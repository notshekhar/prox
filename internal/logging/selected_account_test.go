package logging

import "testing"

func TestAccountLabel(t *testing.T) {
	for id, want := range map[string]string{
		"claude-f38fbc47-someone@gmail.com.json": "someone@gmail.com",
		"codex-48e6e7bd-a.b@oboe.chat-plus.json": "a.b@oboe.chat",
		"antigravity-me@example.co.uk.json":      "me@example.co.uk",
		"claude:apikey:1a2b3c":                   "claude:apikey:1a2b3c",
	} {
		if got := accountLabel(id); got != want {
			t.Errorf("accountLabel(%q) = %q, want %q", id, got, want)
		}
	}
}
