package proxcli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

func (s sandbox) storedSecret(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(s.configPath())
	if err != nil {
		t.Fatal(err)
	}
	var cfg settings
	if err = yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Management.SecretKey
}

func (s sandbox) managementKeyPath() string {
	return filepath.Join(s.home, ".prox", "management.key")
}

func TestFirstStartStoresOnlyAHashOfTheChosenPassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)

	if outcome, _, errOut := run("start"); outcome.Done {
		t.Fatalf("not started: %s", errOut)
	}

	stored := s.storedSecret(t)
	if strings.Contains(stored, sandboxPassword) {
		t.Fatal("password stored in plain text")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(sandboxPassword)); err != nil {
		t.Fatalf("stored hash does not match: %v", err)
	}
}

func TestStartRefusesTheWrongPassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")

	t.Setenv(masterPasswordEnv, "not-the-password")
	outcome, _, errOut := run("start")
	if !outcome.Done || outcome.ExitCode == 0 {
		t.Fatalf("started with a wrong password: %+v", outcome)
	}
	if !strings.Contains(errOut, "wrong master password") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestStartAcceptsTheRightPasswordAgain(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	hash := s.storedSecret(t)

	if outcome, _, errOut := run("start"); outcome.Done {
		t.Fatalf("not started: %s", errOut)
	}
	if s.storedSecret(t) != hash {
		t.Fatal("a normal start must not change the stored password")
	}
}

func TestStartWithoutAnyPasswordSourceRefuses(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	t.Setenv(masterPasswordEnv, "")
	s.fakeTool(t, "security", "exit 44")

	outcome, _, errOut := run("start")
	if !outcome.Done || outcome.ExitCode == 0 {
		t.Fatalf("started with no password: %+v", outcome)
	}
	if !strings.Contains(errOut, masterPasswordEnv) {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestBackgroundStartUnlocksFromKeychain(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	t.Setenv(masterPasswordEnv, "")
	s.fakeTool(t, "security", "echo "+sandboxPassword)

	if outcome, _, errOut := run("start"); outcome.Done {
		t.Fatalf("not started: %s", errOut)
	}
}

func TestOlderInstallMovesFromGeneratedKeyToMasterPassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	if err := os.WriteFile(s.managementKeyPath(), []byte("generated\n"), privateFile); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(s.configPath())
	body := "management:\n  secret-key: \"$2a$10$old\" # hashed on startup\n" + string(raw)
	if err := os.WriteFile(s.configPath(), []byte(body), privateFile); err != nil {
		t.Fatal(err)
	}

	if outcome, _, errOut := run("start"); outcome.Done {
		t.Fatalf("not started: %s", errOut)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(s.storedSecret(t)), []byte(sandboxPassword)); err != nil {
		t.Fatalf("new password not stored: %v", err)
	}
	if _, err := os.Stat(s.managementKeyPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old key still there: %v", err)
	}
	updated, _ := os.ReadFile(s.configPath())
	if !strings.Contains(string(updated), "# hashed on startup") {
		t.Fatal("config comment lost")
	}
}

func TestNewPasswordRules(t *testing.T) {
	long := strings.Repeat("x", maxMasterPasswordBytes+1)
	for name, answers := range map[string][]string{
		"too short": {"short"},
		"too long":  {long},
		"mismatch":  {"long-enough", "different-one"},
	} {
		t.Run(name, func(t *testing.T) {
			input := passwordInput{ask: scripted(answers), attempts: 1}
			if _, err := askNewPassword(input); !errors.Is(err, errInvalidNewPassword) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestTypoCanBeRetriedAtTheTerminal(t *testing.T) {
	cfg := settings{}
	hash, err := bcrypt.GenerateFromPassword([]byte(sandboxPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Management.SecretKey = string(hash)
	input := passwordInput{ask: scripted([]string{"typo", sandboxPassword}), attempts: terminalAttempts}

	if got, err := verifyMasterPassword(cfg, input, &strings.Builder{}); err != nil || got != sandboxPassword {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestPlainSecretFromAHandEditStillUnlocks(t *testing.T) {
	if !matchesMasterPassword("typed-by-hand", "typed-by-hand") {
		t.Fatal("plain secret rejected")
	}
	if matchesMasterPassword("typed-by-hand", "wrong") {
		t.Fatal("wrong password accepted")
	}
}

func TestServiceOnKeepsThePasswordOutOfProcessArguments(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	args, stdin := filepath.Join(s.home, "args"), filepath.Join(s.home, "stdin")
	s.fakeTool(t, "security", `echo "$@" >> `+args+`
if [ "$1" = "-i" ]; then cat >> `+stdin+`; else echo `+sandboxPassword+`; fi`)
	s.fakeTool(t, "launchctl", "exit 0")

	if outcome, _, errOut := run("service", "on"); outcome.ExitCode != 0 {
		t.Fatalf("exit %d: %s", outcome.ExitCode, errOut)
	}
	if got, _ := os.ReadFile(args); strings.Contains(string(got), sandboxPassword) {
		t.Fatalf("password on the command line: %q", got)
	}
	if got, _ := os.ReadFile(stdin); !strings.Contains(string(got), sandboxPassword) {
		t.Fatalf("password not saved: %q", got)
	}
}

func TestServiceOnFailsWhenTheKeychainDidNotKeepThePassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	s.fakeTool(t, "security", "exit 0")
	launched := filepath.Join(s.home, "launched")
	s.fakeTool(t, "launchctl", "touch "+launched)

	if outcome, _, _ := run("service", "on"); outcome.ExitCode == 0 {
		t.Fatal("reported success without a stored password")
	}
	if _, err := os.Stat(launched); err == nil {
		t.Fatal("service started anyway")
	}
}

func TestServiceOnRefusesTheWrongPassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	t.Setenv(masterPasswordEnv, "not-the-password")
	launched := filepath.Join(s.home, "launched")
	s.fakeTool(t, "security", "exit 0")
	s.fakeTool(t, "launchctl", "touch "+launched)

	if outcome, _, _ := run("service", "on"); outcome.ExitCode == 0 {
		t.Fatal("service turned on with a wrong password")
	}
	if _, err := os.Stat(launched); err == nil {
		t.Fatal("service started anyway")
	}
}

func TestDoubleQuoted(t *testing.T) {
	if got := doubleQuoted(`a"b\c`); got != `"a\"b\\c"` {
		t.Fatalf("got %s", got)
	}
}

func scripted(answers []string) func(string) (string, error) {
	return func(string) (string, error) {
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}
}
