package proxcli

import (
	"os"
	"strings"
	"testing"
)

func TestAccountsSkipALoginThatCantBeRead(t *testing.T) {
	s := newSandbox(t)
	s.writeLogin(t, "claude-a.json", claudeLogin("a@example.com"))
	locked := s.writeLogin(t, "claude-b.json", claudeLogin("b@example.com"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, privateFile) })

	outcome, out, errOut := run("accounts")
	if outcome.ExitCode != 0 || !strings.Contains(out, "a@example.com") {
		t.Fatalf("exit %d, out %q, err %q", outcome.ExitCode, out, errOut)
	}
}
