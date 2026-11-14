package proxcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (s sandbox) writeLogin(t *testing.T, name, body string) string {
	t.Helper()
	dir := filepath.Join(s.home, ".prox", "auth")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), privateFile); err != nil {
		t.Fatal(err)
	}
	return path
}

func (s sandbox) writeLog(t *testing.T, lines ...string) {
	t.Helper()
	dir := filepath.Join(s.home, ".prox", "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.log"), []byte(strings.Join(lines, "\n")), privateFile); err != nil {
		t.Fatal(err)
	}
}

func claudeLogin(email string) string {
	return fmt.Sprintf(`{"type":"claude","email":%q,"refresh_token":"secret-refresh-token","access_token":"secret-access-token","expired":%q}`,
		email, time.Now().Add(time.Hour).Format(time.RFC3339))
}

func requestLine(email string) string {
	return `[2026-11-07 13:00:35] [3fe7cd0b] [info ] [gin_logger.go:117] 200 |  1.7s |  127.0.0.1 | POST    "/v1/messages?beta=true" [` + email + `]`
}

func TestDoctorNeverPrintsTokens(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	_, out, _ := run("doctor")
	if strings.Contains(out, "secret-") {
		t.Fatalf("token in report: %s", out)
	}
}

func TestDoctorIsHappyWithTwoHealthyAccountsAndTraffic(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	s.writeLogin(t, "claude-b.json", claudeLogin("b@example.com"))
	s.writeLog(t, requestLine("a@example.com"), requestLine("b@example.com"), requestLine("b@example.com"))

	outcome, out, _ := run("doctor")
	if outcome.ExitCode != 0 || !strings.Contains(out, "No problems found") {
		t.Fatalf("exit %d: %s", outcome.ExitCode, out)
	}
	if !strings.Contains(out, "3 in the recent log") {
		t.Fatalf("out = %s", out)
	}
}

func TestDoctorExplainsARejectedLogin(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	login := s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	s.writeLogin(t, "claude-b.json", claudeLogin("b@example.com"))
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(login, old, old); err != nil {
		t.Fatal(err)
	}
	failed := time.Now().Add(-time.Minute).Format(logTimeLayout)
	s.writeLog(t, requestLine("b@example.com"),
		"["+failed+"] [--------] [warn ] credential refresh failed for claude (claude-a.json): invalid_grant; retaining")

	outcome, out, _ := run("doctor")
	if outcome.ExitCode == 0 || !strings.Contains(out, "prox login claude") {
		t.Fatalf("exit %d: %s", outcome.ExitCode, out)
	}
	if !strings.Contains(out, "Only 1 of 2 claude accounts") {
		t.Fatalf("a rejected login still counted as usable: %s", out)
	}
}

func TestDoctorForgetsARejectionAfterANewLogin(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	failed := time.Now().Add(-time.Hour).Format(logTimeLayout)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	s.writeLogin(t, "claude-b.json", claudeLogin("b@example.com"))
	s.writeLog(t, requestLine("a@example.com"),
		"["+failed+"] [--------] [warn ] credential refresh failed for claude (claude-a.json): invalid_grant")

	if _, out, _ := run("doctor"); strings.Contains(out, "rejected") {
		t.Fatalf("old rejection still reported: %s", out)
	}
}

func TestDoctorSaysWhenNothingUsesProx(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	_, out, _ := run("doctor")
	for _, want := range []string{"prox is stopped", "No requests have reached prox"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in: %s", want, out)
		}
	}
}

func TestDoctorDoesNotCallASingleAccountAProblem(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	s.writeLog(t, requestLine("a@example.com"))

	if outcome, out, _ := run("doctor"); outcome.ExitCode != 0 {
		t.Fatalf("false alarm: %s", out)
	}
}

func TestDoctorCatchesAnExpiredTokenWhileProxIsRunning(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	expired := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	s.writeLogin(t, "claude-b.json", fmt.Sprintf(`{"type":"claude","email":"b@example.com","refresh_token":"x-token","expired":%q}`, expired))
	s.writeLog(t, requestLine("a@example.com"))

	outcome, out, _ := run("doctor")
	if outcome.ExitCode == 0 || !strings.Contains(out, "expired token") || !strings.Contains(out, "Only 1 of 2 claude") {
		t.Fatalf("exit %d: %s", outcome.ExitCode, out)
	}
}

func TestServerFlagsNeedTheMasterPassword(t *testing.T) {
	s := newSandbox(t)
	s.configureStoppedProxy(t)
	run("start")
	t.Setenv(masterPasswordEnv, "not-the-password")
	outcome, _, errOut := run("-tui")
	if !outcome.Done || outcome.ExitCode == 0 {
		t.Fatalf("server flags ran with a wrong password: %+v %s", outcome, errOut)
	}
}

func TestDoctorCountsCodexTraffic(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLogin(t, "codex-a.json", `{"type":"codex","email":"a@example.com","refresh_token":"x-token"}`)
	s.writeLog(t, `[2026-11-07 13:00:35] [3fe7cd0b] [info ] [gin_logger.go:117] 200 |  1.7s |  127.0.0.1 | POST    "/v1/responses" [a@example.com]`)

	if _, out, _ := run("doctor"); strings.Contains(out, "No requests have reached prox") {
		t.Fatalf("Codex traffic not counted: %s", out)
	}
}

func TestDoctorTreatsANullRefreshTokenAsMissing(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	expires := time.Now().Add(time.Hour).Format(time.RFC3339)
	s.writeLogin(t, "claude-b.json", fmt.Sprintf(`{"type":"claude","email":"b@example.com","refresh_token":null,"expired":%q}`, expires))
	s.writeLog(t, requestLine("a@example.com"))

	if _, out, _ := run("doctor"); !strings.Contains(out, "Only 1 of 2 claude") {
		t.Fatalf("a null refresh token counted as usable: %s", out)
	}
}

func TestDoctorCountsLoginsThatNeverExpireAsUsable(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLogin(t, "codex-a.json", `{"type":"codex","email":"a@example.com","api_key":"secret-a"}`)
	s.writeLogin(t, "codex-b.json", `{"type":"codex","email":"b@example.com","api_key":"secret-b"}`)
	s.writeLog(t, requestLine("a@example.com"))

	if outcome, out, _ := run("doctor"); outcome.ExitCode != 0 {
		t.Fatalf("false alarm: %s", out)
	}
}

func TestDoctorIgnoresThePartialLineAtTheStartOfTheTail(t *testing.T) {
	s := newSandbox(t)
	s.listenOnConfiguredPort(t)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	// One line longer than the tail, whose end looks like a request: only the
	// final whole line may be counted.
	partial := strings.Repeat("x", doctorLogTailBytes) + ` POST "/v1/messages"`
	s.writeLog(t, partial, requestLine("a@example.com"))

	if _, out, _ := run("doctor"); !strings.Contains(out, "1 in the recent log") {
		t.Fatalf("out = %s", out)
	}
}
