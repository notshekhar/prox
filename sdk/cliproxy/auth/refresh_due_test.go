package auth

import (
	"context"
	"testing"
	"time"
)

const refreshDueProvider = "refresh-due-test"

func registerRefreshable(t *testing.T, manager *Manager, id string, expires time.Time) {
	t.Helper()
	auth := &Auth{
		ID:       id,
		Provider: refreshDueProvider,
		Status:   StatusActive,
		Metadata: map[string]any{
			"access_token":  "access-" + id,
			"refresh_token": "refresh-" + id,
			"expires_at":    expires.Format(time.RFC3339),
		},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshDueLeavesHealthyLoginsAlone(t *testing.T) {
	executor := &countingRefreshExecutor{id: refreshDueProvider}
	manager := NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	now := time.Now()
	registerRefreshable(t, manager, "healthy", now.Add(6*time.Hour))
	registerRefreshable(t, manager, "expired", now.Add(-time.Hour))

	results := manager.RefreshDue(context.Background())

	if len(results) != 1 || results[0].ID != "expired" {
		t.Fatalf("results = %+v", results)
	}
}

func TestForceRefreshAllStillRefreshesEverything(t *testing.T) {
	executor := &countingRefreshExecutor{id: refreshDueProvider}
	manager := NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	now := time.Now()
	registerRefreshable(t, manager, "healthy", now.Add(6*time.Hour))
	registerRefreshable(t, manager, "expired", now.Add(-time.Hour))

	if results := manager.ForceRefreshAll(context.Background()); len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
}
