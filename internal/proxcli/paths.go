package proxcli

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths holds every location prox reads or writes, so tests can point all of
// them at a temporary directory instead of the user's real home.
type Paths struct {
	Home          string // prox's own state: config, accounts and logs
	Config        string
	Auth          string // account logins
	ManagementKey string // left by older versions, removed once a master password is set
	ClaudeDir     string // CLAUDE_CONFIG_DIR used by `prox claude`
	ServiceEnv    string // the Linux service's password, readable only by this user
	SystemdUnit   string
	LaunchAgent   string
	Log           string
	Logs          string // full server logs, kept out of the terminal
	LegacyAuth    string // where logins lived before prox had its own folder
	ImportMarker  string
}

// DefaultPaths resolves paths from $HOME, with $PROX_HOME overriding the state folder.
func DefaultPaths() (Paths, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("find home folder: %w", err)
	}
	home := os.Getenv("PROX_HOME")
	if home == "" {
		home = filepath.Join(userHome, ".prox")
	}
	return Paths{
		Home:          home,
		Config:        filepath.Join(home, "config.yaml"),
		Auth:          filepath.Join(home, "auth"),
		ManagementKey: filepath.Join(home, "management.key"),
		ClaudeDir:     filepath.Join(userHome, ".claude-hub"),
		ServiceEnv:    filepath.Join(home, "service.env"),
		SystemdUnit:   filepath.Join(userHome, ".config", "systemd", "user", systemdUnitName),
		LaunchAgent:   filepath.Join(userHome, "Library", "LaunchAgents", serviceLabel+".plist"),
		Log:           filepath.Join(home, "prox.log"),
		Logs:          filepath.Join(home, "logs"),
		LegacyAuth:    filepath.Join(userHome, ".cli-proxy-api"),
		ImportMarker:  filepath.Join(home, ".legacy-accounts-imported"),
	}, nil
}
