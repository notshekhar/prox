package logging

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// ReadyAddressField marks the log entry announcing the server is listening.
	ReadyAddressField = "ready_address"
	requestField      = "compact_request"
	// maxErrorWidth keeps a long upstream error body to one readable line.
	maxErrorWidth = 160
	clockFormat   = "15:04:05"
	ansiReset     = "\x1b[0m"
	ansiDim       = "\x1b[2m"
	ansiGreen     = "\x1b[32m"
	ansiYellow    = "\x1b[33m"
	ansiRed       = "\x1b[31m"
)

// CompactOptions configures the tidy terminal log. Full logs go to LogDir.
type CompactOptions struct {
	Out     io.Writer
	Color   bool
	LogDir  string
	OnReady func(address string)
}

// compactRequest is what the terminal shows for one HTTP request. It holds no
// query string, because a query can carry an API key.
type compactRequest struct {
	method  string
	path    string
	status  int
	latency time.Duration
	account string
}

type compactTerminal struct {
	options CompactOptions
	mu      sync.Mutex
}

var compact atomic.Pointer[compactTerminal]

// EnableCompactTerminal replaces the raw log stream on the terminal with one
// line per AI request plus errors, and sends every log line to files instead.
func EnableCompactTerminal(options CompactOptions) {
	terminal := &compactTerminal{options: options}
	compact.Store(terminal)
	log.AddHook(terminal)
	// Nothing may reach the terminal before ConfigureLogOutput opens the file.
	log.SetOutput(io.Discard)
}

// CompactTerminalEnabled reports whether the tidy terminal log is on.
func CompactTerminalEnabled() bool { return compact.Load() != nil }

// ReportStopped ends the terminal log after the server has shut down.
func ReportStopped() {
	if terminal := compact.Load(); terminal != nil {
		terminal.write("prox stopped")
	}
}

// Levels implements log.Hook.
func (t *compactTerminal) Levels() []log.Level { return log.AllLevels }

// Fire implements log.Hook.
func (t *compactTerminal) Fire(entry *log.Entry) error {
	if address, ok := entry.Data[ReadyAddressField].(string); ok && t.options.OnReady != nil {
		t.options.OnReady(address)
		return nil
	}
	if line, ok := compactLine(entry, t.options.Color); ok {
		t.write(line)
	}
	return nil
}

func (t *compactTerminal) write(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = fmt.Fprintln(t.options.Out, line)
}

// compactLine decides what reaches the terminal: AI requests and real errors.
// Dashboard traffic, routine successes such as model listings, startup chatter
// and warnings stay in the log file.
func compactLine(entry *log.Entry, color bool) (string, bool) {
	clock := paint(entry.Time.Format(clockFormat), ansiDim, color)
	if request, ok := entry.Data[requestField].(compactRequest); ok {
		if !request.worthShowing() {
			return "", false
		}
		status := paint(fmt.Sprintf("%d", request.status), statusColor(request.status), color)
		line := fmt.Sprintf("%s  %s  %-6s  %s %s", clock, status, shortLatency(request.latency), request.method, request.path)
		if request.account != "" {
			line += "  " + paint(request.account, ansiDim, color)
		}
		return line, true
	}
	if entry.Level > log.ErrorLevel {
		return "", false
	}
	return fmt.Sprintf("%s  %s  %s", clock, paint("error", ansiRed, color), oneLine(entry.Message)), true
}

func (r compactRequest) worthShowing() bool {
	if r.status >= http.StatusInternalServerError {
		return true
	}
	if !isAIAPIPath(r.path) {
		return false
	}
	return r.method != http.MethodGet || r.status >= http.StatusBadRequest
}

func statusColor(status int) string {
	switch {
	case status >= http.StatusInternalServerError:
		return ansiRed
	case status >= http.StatusBadRequest:
		return ansiYellow
	default:
		return ansiGreen
	}
}

func shortLatency(latency time.Duration) string {
	if latency < time.Second {
		return latency.Round(time.Millisecond).String()
	}
	return latency.Round(100 * time.Millisecond).String()
}

func oneLine(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) <= maxErrorWidth {
		return message
	}
	return message[:maxErrorWidth] + "…"
}

func paint(text, code string, color bool) string {
	if !color {
		return text
	}
	return code + text + ansiReset
}
