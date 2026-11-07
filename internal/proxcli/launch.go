package proxcli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
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

func openDashboard(cfg settings, out, errOut io.Writer) int {
	if !isReachable(cfg) {
		fmt.Fprintln(errOut, "prox is stopped. Start it with: prox start")
		return 1
	}
	url := cfg.baseURL() + dashboardPath
	if err := exec.Command("open", url).Run(); err != nil {
		fmt.Fprintf(out, "Open %s\n", url)
	}
	return 0
}
