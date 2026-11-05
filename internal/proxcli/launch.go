package proxcli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
)

const dashboardPath = "/dashboard"

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

	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintf(errOut, "start claude: %v\n", err)
		return 1
	}
	return 0
}

// openDashboard copies the key first so it is ready to paste on the login screen.
func openDashboard(p Paths, cfg settings, out, errOut io.Writer) int {
	if !isReachable(cfg) {
		fmt.Fprintln(errOut, "prox is stopped. Start it with: prox start")
		return 1
	}
	url := cfg.baseURL() + dashboardPath
	if err := copyManagementKey(p); err != nil {
		fmt.Fprintf(errOut, "Could not copy the key: %v\n", err)
	} else {
		fmt.Fprintln(out, "Management key copied. Paste it on the login screen.")
	}
	if err := exec.Command("open", url).Run(); err != nil {
		fmt.Fprintf(out, "Open %s\n", url)
	}
	return 0
}

func copyManagementKey(p Paths) error {
	raw, err := os.ReadFile(p.ManagementKey)
	if err != nil {
		return err
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(strings.TrimSpace(string(raw)))
	return cmd.Run()
}
