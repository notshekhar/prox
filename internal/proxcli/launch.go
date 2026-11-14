package proxcli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/skratchdot/open-golang/open"
)

const dashboardPath = "/dashboard"

// forwardedSignals end prox itself, for example a closed terminal or a kill.
// Claude Code gets them too, or it would keep running with no parent.
var forwardedSignals = []os.Signal{syscall.SIGTERM, syscall.SIGHUP}

// runClaude keeps prox alive as the parent, so Ctrl+C has to reach Claude Code
// only: claude handles it itself and prox waits for its exit code.
func runClaude(p Paths, cfg settings, args []string, errOut io.Writer) int {
	if !isReachable(cfg) {
		fmt.Fprintln(errOut, "prox is stopped. Start it with: prox start")
		return 1
	}
	cmd := exec.Command("claude", args...)
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+p.ClaudeDir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(errOut, "start claude: %v\n", err)
		return 1
	}
	stopForwarding := forwardSignals(cmd.Process)
	defer stopForwarding()
	return exitCode(cmd.Wait())
}

// forwardSignals passes forwardedSignals on to child until the returned stop is called.
func forwardSignals(child *os.Process) (stop func()) {
	received := make(chan os.Signal, 1)
	signal.Notify(received, forwardedSignals...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-received:
				_ = child.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(received)
		close(done)
	}
}

func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func openDashboard(cfg settings, out, errOut io.Writer) int {
	if !isReachable(cfg) {
		fmt.Fprintln(errOut, "prox is stopped. Start it with: prox start")
		return 1
	}
	url := cfg.baseURL() + dashboardPath
	// open picks the platform's opener: `open` on Linux is often openvt, not a browser.
	if err := open.Run(url); err != nil {
		fmt.Fprintf(out, "Open %s\n", url)
	}
	return 0
}
