package proxcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// sandbox points HOME, PROX_HOME and PATH at a temp folder, so no test can
// reach the user's real config, accounts or tools.
type sandbox struct {
	home string
	bin  string
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PROX_HOME", filepath.Join(home, ".prox"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("MANAGEMENT_STATIC_PATH", "")
	return sandbox{home: home, bin: bin}
}

func (s sandbox) fakeTool(t *testing.T, name, script string) {
	t.Helper()
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(s.bin, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (s sandbox) configPath() string { return filepath.Join(s.home, ".prox", "config.yaml") }

// listenOnConfiguredPort stands in for a running proxy.
func (s sandbox) listenOnConfiguredPort(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	s.writeConfig(t, listener.Addr().(*net.TCPAddr).Port)
}

// configureStoppedProxy uses a port known to be closed, because the default
// port may have the user's real proxy on it.
func (s sandbox) configureStoppedProxy(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	s.writeConfig(t, port)
}

func (s sandbox) writeConfig(t *testing.T, port int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.configPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("server:\n  host: \"127.0.0.1\"\n  port: %d\naccess:\n  api-keys:\n    - \"client-key\"\n", port)
	if err := os.WriteFile(s.configPath(), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func run(args ...string) (Outcome, string, string) {
	var out, errOut bytes.Buffer
	outcome := Dispatch(args, &out, &errOut)
	return outcome, out.String(), errOut.String()
}

func TestFirstRunCreatesPrivateConfigBoundToLocalhost(t *testing.T) {
	s := newSandbox(t)
	if outcome, _, errOut := run("accounts"); outcome.ExitCode != 0 {
		t.Fatalf("exit %d: %s", outcome.ExitCode, errOut)
	}

	info, err := os.Stat(s.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != privateFile {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
	paths, _ := DefaultPaths()
	cfg, err := loadSettings(paths)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Fatalf("host = %q", cfg.Server.Host)
	}
	raw, _ := os.ReadFile(s.configPath())
	if !strings.Contains(string(raw), "disable-auto-update-panel: true") {
		t.Fatal("auto-update would overwrite the bundled dashboard")
	}

	var claude struct {
		Env map[string]string `json:"env"`
	}
	raw, err = os.ReadFile(filepath.Join(s.home, ".claude-hub", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &claude); err != nil {
		t.Fatal(err)
	}
	if claude.Env["ANTHROPIC_AUTH_TOKEN"] != cfg.Access.APIKeys[0] {
		t.Fatal("Claude settings carry a different key from the config")
	}
	if claude.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8317" {
		t.Fatalf("base URL = %q", claude.Env["ANTHROPIC_BASE_URL"])
	}
}

func TestSetupKeepsExistingFiles(t *testing.T) {
	s := newSandbox(t)
	run("accounts")
	settingsPath := filepath.Join(s.home, ".claude-hub", "settings.json")
	for _, path := range []string{s.configPath(), settingsPath} {
		before, _ := os.ReadFile(path)
		run("accounts")
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("%s changed on second run", path)
		}
	}
}

func TestAccountsNeverShowTokens(t *testing.T) {
	s := newSandbox(t)
	dir := filepath.Join(s.home, ".prox", "auth")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	login := `{"type":"claude","email":"me@example.com","access_token":"very-secret","disabled":true}`
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(login), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, out, _ := run("accounts")
	if !strings.Contains(out, "me@example.com") || !strings.Contains(out, "(off)") {
		t.Fatalf("output = %q", out)
	}
	if strings.Contains(out, "very-secret") {
		t.Fatal("token printed")
	}
	if _, out, _ = run("status"); !strings.Contains(out, "0 of 1 accounts on") {
		t.Fatalf("status = %q", out)
	}
}

func TestClaudeRefusesWhenProxyIsStopped(t *testing.T) {
	s := newSandbox(t)
	marker := filepath.Join(s.home, "ran")
	s.fakeTool(t, "claude", "touch "+marker)
	s.configureStoppedProxy(t)

	outcome, _, errOut := run("claude")
	if outcome.ExitCode != 1 || !strings.Contains(errOut, "prox start") {
		t.Fatalf("exit %d: %q", outcome.ExitCode, errOut)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("claude ran without a proxy")
	}
}

func TestClaudeRunsWithHubConfigAndKeepsExitCode(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	record := filepath.Join(s.home, "record")
	s.fakeTool(t, "claude", fmt.Sprintf(`echo "$CLAUDE_CONFIG_DIR $*" > %s; exit 7`, record))

	outcome, _, _ := run("claude", "--resume", "abc")
	if outcome.ExitCode != 7 {
		t.Fatalf("exit = %d", outcome.ExitCode)
	}
	got, _ := os.ReadFile(record)
	want := filepath.Join(s.home, ".claude-hub") + " --resume abc\n"
	if string(got) != want {
		t.Fatalf("claude saw %q, want %q", got, want)
	}
}

func TestDashboardCopiesKeyAndOpensPanel(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	if err := os.WriteFile(filepath.Join(s.home, ".prox", "management.key"), []byte("panel-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clip, opened := filepath.Join(s.home, "clip"), filepath.Join(s.home, "opened")
	s.fakeTool(t, "pbcopy", "cat > "+clip)
	s.fakeTool(t, "open", fmt.Sprintf(`echo "$1" > %s`, opened))

	outcome, out, _ := run("dashboard")
	if outcome.ExitCode != 0 {
		t.Fatalf("exit = %d", outcome.ExitCode)
	}
	if strings.Contains(out, "panel-key") {
		t.Fatal("key printed to the terminal")
	}
	if got, _ := os.ReadFile(clip); string(got) != "panel-key" {
		t.Fatalf("clipboard = %q", got)
	}
	if got, _ := os.ReadFile(opened); !strings.HasSuffix(strings.TrimSpace(string(got)), dashboardPath) {
		t.Fatalf("opened %q", got)
	}
}

func TestStartHandsConfigToServer(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	outcome, _, errOut := run("start")
	if outcome.Done {
		t.Fatalf("server not started: %s", errOut)
	}
	if !slices.Equal(outcome.ServerArgs, []string{"-config", s.configPath()}) {
		t.Fatalf("args = %v", outcome.ServerArgs)
	}
	if outcome.Terminal == nil || outcome.Terminal.LogDir != filepath.Join(s.home, ".prox", "logs") {
		t.Fatalf("terminal = %+v", outcome.Terminal)
	}
}

func TestStartServesTheDashboardBuiltIntoTheBinary(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	panel := filepath.Join(s.home, ".prox", "static", panelFileName)
	if err := os.MkdirAll(filepath.Dir(panel), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(panel, []byte("left by an older version"), 0o600); err != nil {
		t.Fatal(err)
	}

	run("start")

	got, err := os.ReadFile(panel)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("<title>prox</title>")) {
		t.Fatal("old dashboard was not replaced")
	}
	if os.Getenv("MANAGEMENT_STATIC_PATH") != panel {
		t.Fatalf("server points at %q", os.Getenv("MANAGEMENT_STATIC_PATH"))
	}
}

func TestBannerSaysWhereEverythingIs(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	paths, _ := DefaultPaths()
	cfg, err := loadSettings(paths)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printBanner(&out, paths, cfg, 1)
	for _, want := range []string{"listening on " + cfg.baseURL(), "1 account", dashboardPath, "prox claude", "main.log"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("banner missing %q:\n%s", want, out.String())
		}
	}
}

func TestStartRefusesSecondCopy(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	outcome, _, errOut := run("start")
	if !outcome.Done || outcome.ExitCode != 1 || !strings.Contains(errOut, "already running") {
		t.Fatalf("outcome = %+v, %q", outcome, errOut)
	}
}

func TestLoginMapsProviderToServerFlag(t *testing.T) {
	s := newSandbox(t)
	outcome, _, _ := run("login", "claude")
	want := []string{"-config", s.configPath(), "-claude-login", "-no-browser"}
	if !slices.Equal(outcome.ServerArgs, want) {
		t.Fatalf("args = %v", outcome.ServerArgs)
	}
	if outcome, _, _ = run("login", "myspace"); outcome.ExitCode != 2 {
		t.Fatalf("unknown provider exit = %d", outcome.ExitCode)
	}
}

func TestServerFlagsPassThroughWithConfig(t *testing.T) {
	s := newSandbox(t)
	outcome, _, _ := run("-tui")
	if !slices.Equal(outcome.ServerArgs, []string{"-config", s.configPath(), "-tui"}) {
		t.Fatalf("args = %v", outcome.ServerArgs)
	}
	outcome, _, _ = run("-config", "/x.yaml", "-tui")
	if !slices.Equal(outcome.ServerArgs, []string{"-config", "/x.yaml", "-tui"}) {
		t.Fatalf("args = %v", outcome.ServerArgs)
	}
}

func TestUnknownCommandShowsUsage(t *testing.T) {
	newSandbox(t)
	outcome, _, errOut := run("nope")
	if outcome.ExitCode != 2 || !strings.Contains(errOut, "prox start") {
		t.Fatalf("exit %d: %q", outcome.ExitCode, errOut)
	}
}

func (s sandbox) writeLegacyLogin(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(s.home, ".cli-proxy-api")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStartMovesLegacyAccountsIntoProxOnce(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	s.writeLegacyLogin(t, "a.json", `{"type":"claude","email":"old@example.com"}`)
	auth := filepath.Join(s.home, ".prox", "auth")

	if _, _, errOut := run("start"); !strings.Contains(errOut, "Moved 1 account ") {
		t.Fatalf("stderr = %q", errOut)
	}
	info, err := os.Stat(filepath.Join(auth, "a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != privateFile {
		t.Fatalf("login mode = %o", info.Mode().Perm())
	}

	// An account removed in prox must not come back on the next start.
	if err = os.Remove(filepath.Join(auth, "a.json")); err != nil {
		t.Fatal(err)
	}
	run("start")
	if _, err = os.Stat(filepath.Join(auth, "a.json")); err == nil {
		t.Fatal("removed account was imported again")
	}
}

func TestImportNeverOverwritesProxLogins(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	s.writeLegacyLogin(t, "a.json", "old")
	auth := filepath.Join(s.home, ".prox", "auth")
	if err := os.MkdirAll(auth, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(auth, "a.json"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("start")
	if got, _ := os.ReadFile(filepath.Join(auth, "a.json")); string(got) != "new" {
		t.Fatalf("login = %q", got)
	}
}

// A running proxy refreshes tokens in the old folder, so a copy taken now would
// go stale. Nothing may be imported until that proxy has stopped.
func TestImportWaitsWhileAnotherProxyIsRunning(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLegacyLogin(t, "a.json", `{"type":"claude"}`)
	run("start")
	if _, err := os.Stat(filepath.Join(s.home, ".prox", "auth", "a.json")); err == nil {
		t.Fatal("imported while a proxy was running")
	}
}
