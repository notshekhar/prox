package auth

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const (
	// quotaUsageStep treats accounts within five points as equal, so two nearly
	// even accounts keep taking turns instead of one winning on rounding noise.
	quotaUsageStep = 0.05
	// nearLimitUsage is where a conversation leaves its account. It sits just
	// under the limit: moving costs the conversation its prompt cache, so it is
	// only worth paying when a failed request is close.
	nearLimitUsage = 0.95
)

// quotaWindow names the response headers that report one usage window.
type quotaWindow struct {
	usedHeader  string
	resetHeader string
	// fullScale is the value that means 100% used: Claude reports a fraction,
	// Codex a percentage.
	fullScale float64
}

var quotaWindows = []quotaWindow{
	{"Anthropic-Ratelimit-Unified-5h-Utilization", "Anthropic-Ratelimit-Unified-5h-Reset", 1},
	{"Anthropic-Ratelimit-Unified-7d-Utilization", "Anthropic-Ratelimit-Unified-7d-Reset", 1},
	{"X-Codex-Primary-Used-Percent", "X-Codex-Primary-Reset-At", 100},
	{"X-Codex-Secondary-Used-Percent", "X-Codex-Secondary-Reset-At", 100},
}

// LeastUsedSelector sends work to the account with the most quota left, so
// accounts run down evenly. Accounts with no usage seen yet count as unused, and
// equal accounts take turns.
type LeastUsedSelector struct {
	rotation RoundRobinSelector
}

// Pick selects the least-used available auth.
func (s *LeastUsedSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	now := time.Now()
	available, err := getSelectorAvailableAuths(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	return s.rotation.Pick(ctx, provider, model, opts, leastUsedAuths(available, now))
}

func leastUsedAuths(auths []*Auth, now time.Time) []*Auth {
	lowest := math.MaxInt
	least := make([]*Auth, 0, len(auths))
	for _, auth := range auths {
		level := int(authQuotaUsed(auth, now) / quotaUsageStep)
		if level < lowest {
			lowest = level
			least = least[:0]
		}
		if level == lowest {
			least = append(least, auth)
		}
	}
	return least
}

// authQuotaUsed returns the fullest window, from 0 to 1. A window whose reset
// time has passed counts as empty, because the last headers describe the old one.
func authQuotaUsed(auth *Auth, now time.Time) float64 {
	if auth == nil {
		return 0
	}
	used := 0.0
	for _, window := range quotaWindows {
		raw, ok := auth.Quota.Signals[window.usedHeader]
		if !ok || quotaWindowHasReset(auth.Quota.Signals[window.resetHeader], now) {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err == nil && !math.IsNaN(value) {
			used = math.Max(used, value/window.fullScale)
		}
	}
	return used
}

func quotaWindowHasReset(raw string, now time.Time) bool {
	seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return err == nil && seconds > 0 && !time.Unix(seconds, 0).After(now)
}

// shouldLeaveBoundAuth reports whether a conversation should move before its
// account runs out. It stays when every other account is just as full, since
// moving would lose the prompt cache and gain nothing.
func (s *SessionAffinitySelector) shouldLeaveBoundAuth(bound *Auth, candidates []*Auth, now time.Time) bool {
	if _, leastUsed := s.fallback.(*LeastUsedSelector); !leastUsed {
		return false
	}
	if authQuotaUsed(bound, now) < nearLimitUsage {
		return false
	}
	for _, candidate := range candidates {
		if candidate.ID != bound.ID && authQuotaUsed(candidate, now) < nearLimitUsage {
			return true
		}
	}
	return false
}
