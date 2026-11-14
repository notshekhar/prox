package logging

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func request(method, path string, status int) *log.Entry {
	entry := log.NewEntry(log.StandardLogger())
	entry.Level = log.InfoLevel
	entry.Time = time.Date(2026, 11, 5, 14, 2, 11, 0, time.UTC)
	entry.Data = log.Fields{requestField: compactRequest{method: method, path: path, status: status, latency: 1430 * time.Millisecond}}
	return entry
}

func TestCompactLineShowsAIRequestsOnOneLine(t *testing.T) {
	line, ok := compactLine(request(http.MethodPost, "/v1/messages", 200), false)
	if !ok {
		t.Fatal("AI request hidden")
	}
	for _, want := range []string{"14:02:11", "200", "1.4s", "POST /v1/messages"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}

func TestCompactLineFiltersNoise(t *testing.T) {
	cases := []struct {
		name  string
		entry *log.Entry
		shown bool
	}{
		{"dashboard page", request(http.MethodGet, "/management.html", 200), false},
		{"management API", request(http.MethodGet, "/v8/management/config", 200), false},
		{"management auth failure", request(http.MethodGet, "/v8/management/config", 401), false},
		{"model listing", request(http.MethodGet, "/v1/models", 200), false},
		{"rejected AI call", request(http.MethodGet, "/v1/models", 401), true},
		{"rate limited", request(http.MethodPost, "/v1/messages", 429), true},
		{"any server failure", request(http.MethodGet, "/v8/management/config", 500), true},
	}
	for _, tc := range cases {
		if _, shown := compactLine(tc.entry, false); shown != tc.shown {
			t.Errorf("%s: shown = %v", tc.name, shown)
		}
	}
}

func TestCompactLineShowsErrorsButNotChatter(t *testing.T) {
	entry := log.NewEntry(log.StandardLogger())
	entry.Message = "token   refresh\nfailed"
	entry.Level = log.WarnLevel
	if _, shown := compactLine(entry, false); shown {
		t.Fatal("warning shown")
	}
	entry.Level = log.ErrorLevel
	line, shown := compactLine(entry, false)
	if !shown || !strings.Contains(line, "token refresh failed") {
		t.Fatalf("line = %q", line)
	}
}

func TestCompactLineColorIsOptional(t *testing.T) {
	plain, _ := compactLine(request(http.MethodPost, "/v1/messages", 200), false)
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("escape codes in %q", plain)
	}
	colored, _ := compactLine(request(http.MethodPost, "/v1/messages", 500), true)
	if !strings.Contains(colored, ansiRed) {
		t.Fatalf("no red in %q", colored)
	}
}

// Drives the real gin middleware, so a change to what it logs can't silently
// empty the terminal or leak a query string into it.
func TestGinRequestReachesTerminalWithoutQuery(t *testing.T) {
	var out bytes.Buffer
	logger := log.StandardLogger()
	previousHooks, previousOutput := logger.ReplaceHooks(make(log.LevelHooks)), logger.Out
	t.Cleanup(func() {
		logger.ReplaceHooks(previousHooks)
		logger.SetOutput(previousOutput)
	})
	logger.SetOutput(&bytes.Buffer{})
	logger.AddHook(&compactTerminal{options: CompactOptions{Out: &out}})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(GinLogrusLogger())
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/messages?key=secret", nil))

	if !strings.Contains(out.String(), "POST /v1/messages") {
		t.Fatalf("terminal = %q", out.String())
	}
	if strings.Contains(out.String(), "secret") {
		t.Fatalf("query string leaked: %q", out.String())
	}
}

func captureTerminal(t *testing.T) *bytes.Buffer {
	t.Helper()
	out := &bytes.Buffer{}
	logger := log.StandardLogger()
	previousHooks, previousOutput := logger.ReplaceHooks(make(log.LevelHooks)), logger.Out
	t.Cleanup(func() {
		logger.ReplaceHooks(previousHooks)
		logger.SetOutput(previousOutput)
	})
	logger.SetOutput(&bytes.Buffer{})
	logger.AddHook(&compactTerminal{options: CompactOptions{Out: out}})
	return out
}

func TestTerminalNamesTheAccountThatServedARequest(t *testing.T) {
	out := captureTerminal(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(GinLogrusLogger())
	router.POST("/v1/messages", func(c *gin.Context) {
		SetSelectedAccount(c, "claude-f38fbc47-someone@example.com.json")
		c.Status(http.StatusOK)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/messages", nil))

	if !strings.Contains(out.String(), "someone@example.com") {
		t.Fatalf("terminal = %q", out.String())
	}
}

func TestClaudeCodesLaunchProbeStaysOutOfTheLog(t *testing.T) {
	out := captureTerminal(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(GinLogrusLogger())
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodHead, "/api/hello", nil))

	if out.Len() != 0 {
		t.Fatalf("terminal = %q", out.String())
	}
}
