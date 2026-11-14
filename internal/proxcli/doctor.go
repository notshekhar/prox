package proxcli

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/jsoncompat"
)

const (
	// doctorLogLines bounds how much of the log a report reads: the latest
	// failures are the ones that explain today's problem.
	doctorLogLines = 5000
	// doctorLogTailBytes comfortably holds doctorLogLines, so a report never
	// reads the whole log, which can reach the 100 MB log cap.
	doctorLogTailBytes = 4 << 20
	// doctorLookupTimeout keeps a report quick offline; the version line is
	// only a hint there.
	doctorLookupTimeout = 2 * time.Second
)

// apiRequestPaths are the client APIs the proxy serves, so Codex and Gemini
// traffic counts as well as Claude's.
var apiRequestPaths = []string{`"/v1/messages`, `"/v1/responses`, `"/v1/chat/completions`, `"/v1/completions`, `"/v1beta/`}

const logTimeLayout = "2006-01-02 15:04:05"

var (
	accountInLog   = regexp.MustCompile(`\[([^\]\s]+@[^\]\s]+)\]\s*$`)
	refreshFailure = regexp.MustCompile(`^\[([\d-]+ [\d:]+)\].*credential refresh failed for \S+ \(([^)]+)\).*(invalid_grant|unauthorized|refresh token)`)
)

// accountHealth is what doctor needs from a login file. Only the presence of a
// refresh token is read, so a report can never contain one.
type accountHealth struct {
	account
	Expires   string         `json:"expired"`
	Refresh   jsontext.Value `json:"refresh_token"`
	fileName  string
	rejected  bool
	expiresIn time.Duration
	hasExpiry bool
}

func (a accountHealth) label() string {
	if a.Email != "" {
		return a.Email
	}
	return strings.TrimSuffix(a.fileName, ".json")
}

func (a accountHealth) expired() bool { return a.hasExpiry && a.expiresIn <= 0 }

// usable counts a login that never expires, such as an API key, without a
// refresh token; one that does expire needs a refresh token to stay usable.
func (a accountHealth) usable() bool {
	return !a.Disabled && !a.rejected && !a.expired() && (a.hasRefreshToken() || !a.hasExpiry)
}

// hasRefreshToken is false for a missing, empty or null token.
func (a accountHealth) hasRefreshToken() bool {
	var token string
	return json.Unmarshal(a.Refresh, &token, jsoncompat.Decode) == nil && token != ""
}

// logSummary is what the proxy's own log says about recent traffic.
type logSummary struct {
	requests   int
	perAccount map[string]int
	rejectedAt map[string]time.Time // login file name to its latest rejected refresh
}

type doctorReport struct {
	running  bool
	address  string
	strategy string
	affinity bool
	latest   string
	accounts []accountHealth
	log      logSummary
}

func doctor(p Paths, cfg settings, out io.Writer) int {
	dir, err := authDir(p, cfg)
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	report := doctorReport{
		running:  isReachable(cfg),
		address:  cfg.baseURL(),
		strategy: cfg.Routing.Strategy,
		affinity: cfg.Routing.SessionAffinity,
		latest:   fetchLatestTag(doctorLookupTimeout),
		log:      readLogSummary(filepath.Join(p.Logs, "main.log")),
	}
	report.accounts = readAccountHealth(dir, report.log.rejectedAt, time.Now())
	printReport(out, report)
	problems := diagnose(report)
	if len(problems) == 0 {
		fmt.Fprintln(out, "\nNo problems found.")
		return 0
	}
	fmt.Fprintln(out, "\nProblems")
	for _, problem := range problems {
		fmt.Fprintf(out, "  %s\n", problem)
	}
	return 1
}

func readAccountHealth(dir string, rejectedAt map[string]time.Time, now time.Time) []accountHealth {
	logins, _ := readLoginFiles(dir)
	accounts := make([]accountHealth, 0, len(logins))
	for _, login := range logins {
		var health accountHealth
		if json.Unmarshal(login.raw, &health, jsoncompat.Decode) != nil || health.Provider == "" {
			continue
		}
		health.fileName = filepath.Base(login.path)
		// A new login rewrites the file, which settles any earlier rejection.
		if info, errStat := os.Stat(login.path); errStat == nil {
			health.rejected = rejectedAt[health.fileName].After(info.ModTime())
		}
		if expires, errParse := time.Parse(time.RFC3339, health.Expires); errParse == nil {
			health.expiresIn, health.hasExpiry = expires.Sub(now), true
		}
		accounts = append(accounts, health)
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Provider != accounts[j].Provider {
			return accounts[i].Provider < accounts[j].Provider
		}
		return accounts[i].label() < accounts[j].label()
	})
	return accounts
}

func readLogSummary(path string) logSummary {
	summary := logSummary{perAccount: map[string]int{}, rejectedAt: map[string]time.Time{}}
	for _, line := range readLogTail(path) {
		if isAPIRequest(line) {
			summary.requests++
			if match := accountInLog.FindStringSubmatch(line); match != nil {
				summary.perAccount[match[1]]++
			}
		}
		if match := refreshFailure.FindStringSubmatch(line); match != nil {
			if at, errParse := time.ParseInLocation(logTimeLayout, match[1], time.Local); errParse == nil {
				summary.rejectedAt[match[2]] = at
			}
		}
	}
	return summary
}

// readLogTail returns up to doctorLogLines whole lines from the end of the log.
func readLogTail(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	start := max(info.Size()-doctorLogTailBytes, 0)
	raw, err := io.ReadAll(io.NewSectionReader(file, start, info.Size()-start))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	if start > 0 {
		lines = lines[1:] // the read began partway through this line
	}
	if len(lines) > doctorLogLines {
		lines = lines[len(lines)-doctorLogLines:]
	}
	return lines
}

func isAPIRequest(line string) bool {
	if !strings.Contains(line, "POST") {
		return false
	}
	for _, path := range apiRequestPaths {
		if strings.Contains(line, path) {
			return true
		}
	}
	return false
}

func printReport(out io.Writer, r doctorReport) {
	fmt.Fprintf(out, "Version    %s\n", versionLine(r.latest))
	if r.running {
		fmt.Fprintf(out, "Proxy      running at %s\n", r.address)
	} else {
		fmt.Fprintln(out, "Proxy      stopped")
	}
	fmt.Fprintf(out, "Routing    %s\n", routingLine(r))
	fmt.Fprintln(out, "Accounts")
	for _, acc := range r.accounts {
		fmt.Fprintf(out, "  %-12s %-34s %s\n", acc.Provider, acc.label(), accountState(acc))
	}
	fmt.Fprintf(out, "Requests   %d in the recent log\n", r.log.requests)
	for _, name := range sortedKeys(r.log.perAccount) {
		fmt.Fprintf(out, "  %-47s %d\n", name, r.log.perAccount[name])
	}
}

func versionLine(latest string) string {
	if latest != "" && isNewerRelease(latest, buildinfo.Version) {
		return fmt.Sprintf("%s (%s is out: prox upgrade)", buildinfo.Version, latest)
	}
	return buildinfo.Version
}

func routingLine(r doctorReport) string {
	strategy := r.strategy
	if strategy == "" {
		strategy = "round-robin"
	}
	if r.affinity {
		return strategy + ", each conversation stays on one account"
	}
	return strategy + ", every request can change account"
}

func accountState(acc accountHealth) string {
	switch {
	case acc.Disabled:
		return "off"
	case acc.rejected:
		return "sign-in rejected"
	case !acc.hasExpiry:
		return "ok"
	case acc.expiresIn <= 0:
		return fmt.Sprintf("token expired %s ago", shortDuration(acc.expiresIn.Abs()))
	default:
		return fmt.Sprintf("token good for %s", shortDuration(acc.expiresIn))
	}
}

func shortDuration(d time.Duration) string {
	minutes := int(d.Round(time.Minute).Minutes())
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
}

// diagnose turns the facts into what to do next, most useful first.
func diagnose(r doctorReport) []string {
	var problems []string
	if !r.running {
		problems = append(problems, "prox is stopped, so tokens aren't being refreshed. Run: prox start")
	}
	for _, acc := range r.accounts {
		if problem := accountProblem(acc, r.running); problem != "" {
			problems = append(problems, problem)
		}
	}
	if len(r.accounts) == 0 {
		problems = append(problems, "No accounts yet. Add one with: prox login claude")
	}
	problems = append(problems, rotationProblems(r.accounts)...)
	if r.log.requests == 0 {
		problems = append(problems, "No requests have reached prox. Open Claude Code with: prox claude, or point another tool at "+r.address)
	}
	return problems
}

func accountProblem(acc accountHealth, running bool) string {
	switch {
	case acc.Disabled:
		return ""
	case acc.rejected:
		return fmt.Sprintf("%s %s was rejected, usually because another machine refreshed the same login. Run: prox login %s",
			acc.Provider, acc.label(), acc.Provider)
	case acc.expired() && running:
		return fmt.Sprintf("%s %s has an expired token that prox hasn't renewed. If it stays that way, run: prox login %s",
			acc.Provider, acc.label(), acc.Provider)
	}
	return ""
}

// rotationProblems only speaks up where rotation was possible and has stopped:
// a provider with one account never had anything to rotate to.
func rotationProblems(accounts []accountHealth) []string {
	total, usable := map[string]int{}, map[string]int{}
	for _, acc := range accounts {
		if acc.Disabled {
			continue
		}
		total[acc.Provider]++
		if acc.usable() {
			usable[acc.Provider]++
		}
	}
	var problems []string
	for _, provider := range sortedKeys(total) {
		if total[provider] > 1 && usable[provider] < 2 {
			problems = append(problems, fmt.Sprintf("Only %d of %d %s accounts can be used, so requests can't rotate.",
				usable[provider], total[provider], provider))
		}
	}
	return problems
}

func sortedKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
