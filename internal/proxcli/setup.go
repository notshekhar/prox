package proxcli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/jsoncompat"
	"gopkg.in/yaml.v3"
)

const (
	defaultPort = 8317
	// secretBytes gives 256 bits of entropy, far beyond guessing for a local key.
	secretBytes = 32
	// privateFile keeps keys readable only by the user who owns them.
	privateFile = 0o600
	privateDir  = 0o700
)

// configTemplate binds to localhost because the upstream default is every
// interface. Auto-update is off so the bundled dashboard is never replaced by
// the upstream release.
const configTemplate = `config-version: 8
server:
  host: "127.0.0.1"
  port: %d
management:
  secret-key: ""
  disable-auto-update-panel: true
access:
  api-keys:
    - %q
routing:
  strategy: "least-used"
  session-affinity: true
  session-affinity-ttl: "1h"
  retry:
    request-retry: 3
    max-retry-interval: 30
oauth:
  auth-dir: %q
observability:
  logs:
    logging-to-file: true
    logs-max-total-size-mb: 100
`

// settings is the part of the proxy config prox itself needs to read.
type settings struct {
	Server struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"server"`
	Management struct {
		SecretKey string `yaml:"secret-key"`
	} `yaml:"management"`
	Access struct {
		APIKeys []string `yaml:"api-keys"`
	} `yaml:"access"`
	Routing struct {
		Strategy        string `yaml:"strategy"`
		SessionAffinity bool   `yaml:"session-affinity"`
	} `yaml:"routing"`
	OAuth struct {
		AuthDir string `yaml:"auth-dir"`
	} `yaml:"oauth"`
}

func (s settings) address() string {
	host := s.Server.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(s.Server.Port))
}

func (s settings) baseURL() string { return "http://" + s.address() }

// ensureSetup creates anything missing and never overwrites what already
// exists, so it is safe to run before every command.
func ensureSetup(p Paths) (settings, error) {
	if err := ensureConfig(p); err != nil {
		return settings{}, err
	}
	cfg, err := loadSettings(p)
	if err != nil {
		return settings{}, err
	}
	if err = ensureClaudeSettings(p, cfg); err != nil {
		return settings{}, err
	}
	return cfg, nil
}

func ensureConfig(p Paths) error {
	if _, err := os.Stat(p.Config); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check config: %w", err)
	}
	if err := os.MkdirAll(p.Home, privateDir); err != nil {
		return fmt.Errorf("create %s: %w", p.Home, err)
	}
	clientKey, err := newSecret()
	if err != nil {
		return err
	}
	body := fmt.Sprintf(configTemplate, defaultPort, clientKey, p.Auth)
	if err = os.WriteFile(p.Config, []byte(body), privateFile); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func loadSettings(p Paths) (settings, error) {
	raw, err := os.ReadFile(p.Config)
	if err != nil {
		return settings{}, fmt.Errorf("read config: %w", err)
	}
	var cfg settings
	if err = yaml.Unmarshal(raw, &cfg); err != nil {
		return settings{}, fmt.Errorf("parse %s: %w", p.Config, err)
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = defaultPort
	}
	return cfg, nil
}

// ensureClaudeSettings points `prox claude` at this proxy. It runs before every
// command because the port and client key can change after setup, and a stale
// copy fails with "connection refused" or 401. Other settings are left alone.
func ensureClaudeSettings(p Paths, cfg settings) error {
	path := filepath.Join(p.ClaudeDir, "settings.json")
	doc, ok := readClaudeSettings(path)
	if !ok || !syncClaudeEnv(doc, cfg) {
		return nil
	}
	if err := os.MkdirAll(p.ClaudeDir, privateDir); err != nil {
		return fmt.Errorf("create %s: %w", p.ClaudeDir, err)
	}
	body, err := json.Marshal(doc, jsoncompat.Encode, jsontext.WithIndent("  "))
	if err != nil {
		return fmt.Errorf("encode Claude settings: %w", err)
	}
	if err = os.WriteFile(path, append(body, '\n'), privateFile); err != nil {
		return fmt.Errorf("write Claude settings: %w", err)
	}
	return nil
}

// readClaudeSettings returns an empty document for a missing file. A file that
// isn't valid JSON is reported as not ok: it is the user's, so it is never replaced.
func readClaudeSettings(path string) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, true
	}
	var doc map[string]any
	if err != nil || json.Unmarshal(raw, &doc, jsoncompat.Decode) != nil || doc == nil {
		return nil, false
	}
	return doc, true
}

// syncClaudeEnv reports whether it changed anything. Any configured client key
// is kept, so adding a second key doesn't rewrite the file. With no client keys
// the proxy asks for none, so the token is left as it is.
func syncClaudeEnv(doc map[string]any, cfg settings) bool {
	env, _ := doc["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
		doc["env"] = env
	}
	changed := false
	set := func(key, value string) {
		if env[key] != value {
			env[key], changed = value, true
		}
	}
	set("ANTHROPIC_BASE_URL", cfg.baseURL())
	keys := cfg.Access.APIKeys
	if token, _ := env["ANTHROPIC_AUTH_TOKEN"].(string); len(keys) > 0 && !slices.Contains(keys, token) {
		set("ANTHROPIC_AUTH_TOKEN", keys[0])
	}
	return changed
}

func newSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
