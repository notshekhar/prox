package proxcli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
  strategy: "round-robin"
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
	OAuth struct {
		AuthDir string `yaml:"auth-dir"`
	} `yaml:"oauth"`
}

func (s settings) address() string {
	host := s.Server.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s:%d", host, s.Server.Port)
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

func ensureClaudeSettings(p Paths, cfg settings) error {
	path := filepath.Join(p.ClaudeDir, "settings.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if len(cfg.Access.APIKeys) == 0 {
		return fmt.Errorf("%s has no access.api-keys entry", p.Config)
	}
	if err := os.MkdirAll(p.ClaudeDir, privateDir); err != nil {
		return fmt.Errorf("create %s: %w", p.ClaudeDir, err)
	}
	body, err := json.MarshalIndent(map[string]map[string]string{"env": {
		"ANTHROPIC_BASE_URL":   cfg.baseURL(),
		"ANTHROPIC_AUTH_TOKEN": cfg.Access.APIKeys[0],
	}}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Claude settings: %w", err)
	}
	if err = os.WriteFile(path, append(body, '\n'), privateFile); err != nil {
		return fmt.Errorf("write Claude settings: %w", err)
	}
	return nil
}

func newSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
