package auth

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func authUsing(id string, used float64) *Auth {
	return &Auth{ID: id, Provider: "claude", Quota: QuotaState{Signals: map[string]string{
		"Anthropic-Ratelimit-Unified-5h-Utilization": strconv.FormatFloat(used, 'f', 2, 64),
	}}}
}

func pickID(t *testing.T, selector Selector, opts cliproxyexecutor.Options, auths ...*Auth) string {
	t.Helper()
	picked, err := selector.Pick(context.Background(), "claude", "m", opts, auths)
	if err != nil {
		t.Fatal(err)
	}
	return picked.ID
}

func TestLeastUsedPicksTheEmptiestAccount(t *testing.T) {
	selector := &LeastUsedSelector{}
	for range 4 {
		if got := pickID(t, selector, cliproxyexecutor.Options{}, authUsing("a", 0.8), authUsing("b", 0.2), authUsing("c", 0.5)); got != "b" {
			t.Fatalf("picked %s", got)
		}
	}
}

func TestLeastUsedTakesTurnsBetweenNearlyEqualAccounts(t *testing.T) {
	selector := &LeastUsedSelector{}
	a, b := authUsing("a", 0.21), authUsing("b", 0.22)
	first := pickID(t, selector, cliproxyexecutor.Options{}, a, b)
	if second := pickID(t, selector, cliproxyexecutor.Options{}, a, b); second == first {
		t.Fatalf("picked %s twice", first)
	}
}

func TestLeastUsedPrefersAnAccountWithNoUsageSeen(t *testing.T) {
	unseen := &Auth{ID: "b", Provider: "claude"}
	if got := pickID(t, &LeastUsedSelector{}, cliproxyexecutor.Options{}, authUsing("a", 0.3), unseen); got != "b" {
		t.Fatalf("picked %s", got)
	}
}

func TestQuotaUsedTakesTheFullestWindowAndIgnoresResetOnes(t *testing.T) {
	now := time.Now()
	future, past := fmt.Sprint(now.Add(time.Hour).Unix()), fmt.Sprint(now.Add(-time.Hour).Unix())
	auth := &Auth{Quota: QuotaState{Signals: map[string]string{
		"Anthropic-Ratelimit-Unified-5h-Utilization": "0.9",
		"Anthropic-Ratelimit-Unified-5h-Reset":       past,
		"Anthropic-Ratelimit-Unified-7d-Utilization": "0.4",
		"Anthropic-Ratelimit-Unified-7d-Reset":       future,
	}}}
	if got := authQuotaUsed(auth, now); got != 0.4 {
		t.Fatalf("used = %v", got)
	}
}

func TestQuotaUsedReadsCodexPercentages(t *testing.T) {
	auth := &Auth{Quota: QuotaState{Signals: map[string]string{"X-Codex-Primary-Used-Percent": "50"}}}
	if got := authQuotaUsed(auth, time.Now()); got != 0.5 {
		t.Fatalf("used = %v", got)
	}
}

func affinityOptions(session string) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{Headers: http.Header{"X-Claude-Code-Session-Id": {session}}}
}

func TestConversationMovesBeforeItsAccountRunsOut(t *testing.T) {
	selector := NewSessionAffinitySelector(&LeastUsedSelector{})
	opts := affinityOptions("11111111-1111-4111-8111-111111111111")
	a, b := authUsing("a", 0.1), authUsing("b", 0.5)
	if got := pickID(t, selector, opts, a, b); got != "a" {
		t.Fatalf("first pick %s", got)
	}
	a = authUsing("a", 0.96)
	if got := pickID(t, selector, opts, a, b); got != "b" {
		t.Fatalf("stayed on %s", got)
	}
	if got := pickID(t, selector, opts, a, b); got != "b" {
		t.Fatalf("did not stay moved: %s", got)
	}
}

func TestConversationStaysWhenEveryAccountIsNearlyFull(t *testing.T) {
	selector := NewSessionAffinitySelector(&LeastUsedSelector{})
	opts := affinityOptions("22222222-2222-4222-8222-222222222222")
	a, b := authUsing("a", 0.1), authUsing("b", 0.97)
	if got := pickID(t, selector, opts, a, b); got != "a" {
		t.Fatalf("first pick %s", got)
	}
	a = authUsing("a", 0.96)
	if got := pickID(t, selector, opts, a, b); got != "a" {
		t.Fatalf("moved to %s for no gain", got)
	}
}

func TestRoundRobinConversationsNeverMoveEarly(t *testing.T) {
	selector := NewSessionAffinitySelector(&RoundRobinSelector{})
	opts := affinityOptions("33333333-3333-4333-8333-333333333333")
	a, b := authUsing("a", 0.1), authUsing("b", 0.1)
	first := pickID(t, selector, opts, a, b)
	if got := pickID(t, selector, opts, authUsing("a", 0.99), authUsing("b", 0.99)); got != first {
		t.Fatalf("moved from %s to %s", first, got)
	}
}
