package proxcli

import (
	json "encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func (s sandbox) readClaudeEnv(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.home, ".claude-hub", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestClaudeSettingsFollowANewPortAndClientKey(t *testing.T) {
	s := newSandbox(t)
	s.writeConfig(t, 8317)
	run("accounts")
	path := filepath.Join(s.home, ".claude-hub", "settings.json")
	if err := os.WriteFile(path, []byte(`{"model":"opus","env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8317","ANTHROPIC_AUTH_TOKEN":"client-key"}}`), privateFile); err != nil {
		t.Fatal(err)
	}
	config := "server:\n  host: \"127.0.0.1\"\n  port: 9400\naccess:\n  api-keys:\n    - \"rotated-key\"\n"
	if err := os.WriteFile(s.configPath(), []byte(config), privateFile); err != nil {
		t.Fatal(err)
	}

	run("accounts")

	doc := s.readClaudeEnv(t)
	env := doc["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:9400" || env["ANTHROPIC_AUTH_TOKEN"] != "rotated-key" {
		t.Fatalf("env = %v", env)
	}
	if doc["model"] != "opus" {
		t.Fatalf("the user's own setting was dropped: %v", doc)
	}
}

func TestClaudeSettingsKeepAnyConfiguredClientKey(t *testing.T) {
	s := newSandbox(t)
	config := fmt.Sprintf("server:\n  port: %d\naccess:\n  api-keys:\n    - \"first\"\n    - \"second\"\n", 8317)
	if err := os.MkdirAll(filepath.Dir(s.configPath()), privateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.configPath(), []byte(config), privateFile); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.home, ".claude-hub")
	if err := os.MkdirAll(dir, privateDir); err != nil {
		t.Fatal(err)
	}
	body := `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8317","ANTHROPIC_AUTH_TOKEN":"second"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), privateFile); err != nil {
		t.Fatal(err)
	}

	run("accounts")

	if got, _ := os.ReadFile(filepath.Join(dir, "settings.json")); string(got) != body {
		t.Fatalf("settings rewritten: %s", got)
	}
}

func TestClaudeSettingsThatArentJSONAreLeftAlone(t *testing.T) {
	s := newSandbox(t)
	s.writeConfig(t, 8317)
	dir := filepath.Join(s.home, ".claude-hub")
	if err := os.MkdirAll(dir, privateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{ half-edited"), privateFile); err != nil {
		t.Fatal(err)
	}

	if outcome, _, errOut := run("accounts"); outcome.ExitCode != 0 {
		t.Fatalf("exit %d: %s", outcome.ExitCode, errOut)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "settings.json")); string(got) != "{ half-edited" {
		t.Fatalf("the user's file was replaced: %s", got)
	}
}

func TestAddressBracketsAnIPv6Host(t *testing.T) {
	var cfg settings
	cfg.Server.Host, cfg.Server.Port = "::1", 8317
	if got := cfg.baseURL(); got != "http://[::1]:8317" {
		t.Fatalf("baseURL = %q", got)
	}
}
