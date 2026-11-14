package proxcli

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/jsoncompat"
)

// reachTimeout is short because the proxy is local: it answers at once or not at all.
const reachTimeout = 400 * time.Millisecond

// account is the non-secret part of a login file. Tokens are never decoded.
type account struct {
	Provider string `json:"type"`
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
}

func authDir(p Paths, cfg settings) (string, error) {
	dir := cfg.OAuth.AuthDir
	if dir == "" {
		return p.Auth, nil
	}
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home folder: %w", err)
		}
		dir = filepath.Join(home, rest)
	}
	return dir, nil
}

// loginFile is one readable file in the auth folder.
type loginFile struct {
	path string
	raw  []byte
}

// readLoginFiles skips a file it can't read instead of failing, so one broken
// login doesn't hide every other account.
func readLoginFiles(dir string) ([]loginFile, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	logins := make([]loginFile, 0, len(paths))
	for _, path := range paths {
		if raw, errRead := os.ReadFile(path); errRead == nil {
			logins = append(logins, loginFile{path: path, raw: raw})
		}
	}
	return logins, nil
}

func listAccounts(p Paths, cfg settings) ([]account, error) {
	dir, err := authDir(p, cfg)
	if err != nil {
		return nil, err
	}
	logins, err := readLoginFiles(dir)
	if err != nil {
		return nil, err
	}
	accounts := make([]account, 0, len(logins))
	for _, login := range logins {
		var acc account
		// A file that isn't a login is skipped rather than failing the whole list.
		if json.Unmarshal(login.raw, &acc, jsoncompat.Decode) != nil || acc.Provider == "" {
			continue
		}
		accounts = append(accounts, acc)
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Provider != accounts[j].Provider {
			return accounts[i].Provider < accounts[j].Provider
		}
		return accounts[i].Email < accounts[j].Email
	})
	return accounts, nil
}

func printAccounts(out io.Writer, accounts []account) {
	if len(accounts) == 0 {
		fmt.Fprintln(out, "No accounts yet. Add one with: prox login claude")
		return
	}
	for _, acc := range accounts {
		state := ""
		if acc.Disabled {
			state = "  (off)"
		}
		fmt.Fprintf(out, "%-8s %s%s\n", acc.Provider, acc.Email, state)
	}
}

func isReachable(cfg settings) bool {
	conn, err := net.DialTimeout("tcp", cfg.address(), reachTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func printStatus(out io.Writer, cfg settings, accounts []account) {
	if isReachable(cfg) {
		fmt.Fprintf(out, "prox is running at %s\n", cfg.baseURL())
	} else {
		fmt.Fprintln(out, "prox is stopped. Start it with: prox start")
	}
	active := 0
	for _, acc := range accounts {
		if !acc.Disabled {
			active++
		}
	}
	fmt.Fprintf(out, "%d of %d accounts on\n", active, len(accounts))
}
