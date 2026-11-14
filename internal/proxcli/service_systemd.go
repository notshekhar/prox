package proxcli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const systemdUnitName = "prox.service"

const systemdUnitTemplate = `[Unit]
Description=prox

[Service]
ExecStart=%s start
Environment=%s
Environment=%s=1
EnvironmentFile=%s
Restart=on-failure
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`

// systemdService runs prox as a systemd user unit. Linux has no one Keychain to
// rely on, so the password sits in a file only this user can read.
type systemdService struct{}

func (systemdService) savePassword(p Paths, password string) error {
	body := fmt.Sprintf("%s=%s\n", masterPasswordEnv, doubleQuoted(password))
	if err := os.WriteFile(p.ServiceEnv, []byte(body), privateFile); err != nil {
		return fmt.Errorf("save service password: %w", err)
	}
	// WriteFile keeps the mode of a file that already exists.
	return os.Chmod(p.ServiceEnv, privateFile)
}

func (systemdService) forgetPassword(p Paths) { _ = removeIfPresent(p.ServiceEnv) }

func (systemdService) enable(p Paths, exe string) error {
	body := fmt.Sprintf(systemdUnitTemplate, systemdCommand(exe), systemdValue(doubleQuoted("PROX_HOME="+p.Home)),
		serviceEnv, systemdValue(p.ServiceEnv), systemdValue(p.Log), systemdValue(p.Log))
	if err := os.MkdirAll(filepath.Dir(p.SystemdUnit), 0o755); err != nil {
		return fmt.Errorf("create systemd folder: %w", err)
	}
	if err := os.WriteFile(p.SystemdUnit, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	return runSystemctl("enable", "--now", systemdUnitName)
}

func (systemdService) disable(p Paths) error {
	_ = runSystemctl("disable", "--now", systemdUnitName)
	if err := removeIfPresent(p.SystemdUnit); err != nil {
		return fmt.Errorf("remove service file: %w", err)
	}
	return runSystemctl("daemon-reload")
}

func runSystemctl(args ...string) error {
	command := append([]string{"--user"}, args...)
	if output, err := exec.Command("systemctl", command...).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %v: %w: %s", args, err, output)
	}
	return nil
}

// systemdValue escapes systemd's % specifiers, which it expands in every
// setting, so a path holding a % stays that path.
func systemdValue(value string) string { return strings.ReplaceAll(value, "%", "%%") }

// systemdCommand quotes a program path for ExecStart, which splits on spaces
// and also expands $ variables.
func systemdCommand(path string) string {
	return systemdValue(strings.ReplaceAll(doubleQuoted(path), "$", "$$"))
}
