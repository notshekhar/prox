package proxcli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

// terminalOptions sets up the tidy server log for `prox start`.
func terminalOptions(p Paths, cfg settings, out *os.File) *logging.CompactOptions {
	return &logging.CompactOptions{
		Out:    out,
		Color:  isTerminal(out) && os.Getenv("NO_COLOR") == "",
		LogDir: p.Logs,
		OnReady: func(string) {
			accounts, err := listAccounts(p, cfg)
			if err != nil {
				accounts = nil
			}
			printBanner(out, p, cfg, len(accounts))
		},
	}
}

func printBanner(out io.Writer, p Paths, cfg settings, accounts int) {
	fmt.Fprintf(out, "prox %s listening on %s (%s)\n", buildinfo.Version, cfg.baseURL(), countAccounts(accounts))
	fmt.Fprintf(out, "  dashboard: %s%s\n", cfg.baseURL(), dashboardPath)
	fmt.Fprintln(out, "  claude:    prox claude")
	fmt.Fprintf(out, "  logs:      %s\n\n", filepath.Join(p.Logs, "main.log"))
}

func countAccounts(count int) string {
	if count == 1 {
		return "1 account"
	}
	return fmt.Sprintf("%d accounts", count)
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
