package proxcli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// serviceEnv marks a `prox start` run by the background service.
const serviceEnv = "PROX_SERVICE"

// giveUp ends `prox start` over a problem that retrying can't fix, such as a
// wrong saved password or a copy already running. Under the background service
// it exits 0, because launchd and systemd restart a failed start every few
// seconds, forever; the reason is still written to the log.
func giveUp(errOut io.Writer, err error) Outcome {
	fmt.Fprintln(errOut, err)
	if os.Getenv(serviceEnv) != "" {
		return finished(0)
	}
	return finished(1)
}

// backgroundService is how one operating system keeps prox running. Nobody is
// at a terminal when it starts at login, so each keeps the master password
// somewhere it can reach by itself.
type backgroundService interface {
	savePassword(p Paths, password string) error
	forgetPassword(p Paths)
	enable(p Paths, exe string) error
	disable(p Paths) error
}

func backgroundServiceFor(goos string) (backgroundService, error) {
	switch goos {
	case "darwin":
		return launchdService{}, nil
	case "linux":
		return systemdService{}, nil
	}
	return nil, fmt.Errorf("prox service isn't supported on %s", goos)
}

func serviceOn(p Paths, cfg settings, out, errOut io.Writer) int {
	if err := turnServiceOn(p, cfg, runtime.GOOS, errOut); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "prox now runs in the background and starts at login. Logs: %s\n", p.Log)
	return 0
}

func turnServiceOn(p Paths, cfg settings, goos string, errOut io.Writer) error {
	manager, err := backgroundServiceFor(goos)
	if err != nil {
		return err
	}
	// Prove the password first, so a typo can't leave the service failing at
	// every login.
	password, err := requireMasterPassword(p, cfg, terminalOrEnvPassword(errOut), errOut)
	if err != nil {
		return err
	}
	if err = manager.savePassword(p, password); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fmt.Errorf("find prox binary: %w", err)
	}
	return manager.enable(p, exe)
}

func serviceOff(p Paths, out, errOut io.Writer) int {
	if err := turnServiceOff(p, runtime.GOOS); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintln(out, "prox no longer runs in the background.")
	return 0
}

func turnServiceOff(p Paths, goos string) error {
	manager, err := backgroundServiceFor(goos)
	if err != nil {
		return err
	}
	manager.forgetPassword(p)
	return manager.disable(p)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
