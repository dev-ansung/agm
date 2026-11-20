package db

import (
	"testing"
	"time"
)

func TestStoreLifecycleAndTargetTracking(t *testing.T) {
	t.Setenv("AGM_DATA_DIR", t.TempDir())

	if err := EnsureStore(); err != nil {
		t.Fatalf("EnsureStore failed: %v", err)
	}

	tok := &Token{
		AccessToken:     "access-1",
		RefreshToken:    "refresh-1",
		ExpiryTimestamp: time.Now().Add(time.Hour).Unix(),
	}
	q := &Quota{
		Models: map[string]ModelQuota{
			"gemini-2.5-flash": {Percentage: 64, ResetTime: "2026-11-21T00:05:25Z"},
		},
	}

	if err := UpsertAccount("user@example.com", "Test User", "", tok, q); err != nil {
		t.Fatalf("UpsertAccount failed: %v", err)
	}

	acc, err := FindAccount("user@example.com")
	if err != nil {
		t.Fatalf("FindAccount failed: %v", err)
	}
	if acc.Token == nil || acc.Token.AccessToken != "access-1" {
		t.Fatalf("expected decrypted token access-1, got %+v", acc.Token)
	}
	if acc.Quota == nil || acc.Quota.Models["gemini-2.5-flash"].Percentage != 64 {
		t.Fatalf("expected quota 64, got %+v", acc.Quota)
	}

	if err := SetActive("user@example.com"); err != nil {
		t.Fatalf("SetActive failed: %v", err)
	}
	if err := SetActiveForTarget("agy", acc.ID); err != nil {
		t.Fatalf("SetActiveForTarget failed: %v", err)
	}
	if got := GetActiveForTarget("agy"); got != acc.ID {
		t.Fatalf("expected active agy %q, got %q", acc.ID, got)
	}

	accounts, err := ListAccounts()
	if err != nil {
		t.Fatalf("ListAccounts failed: %v", err)
	}
	if len(accounts) != 1 || !accounts[0].IsActive {
		t.Fatalf("expected 1 active account, got %+v", accounts)
	}
}
