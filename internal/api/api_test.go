package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuotaEndpointEnvOverride(t *testing.T) {
	t.Setenv("AGM_QUOTA_URL", "")
	if got := QuotaEndpoint(); got != urlQuota {
		t.Fatalf("expected default %q, got %q", urlQuota, got)
	}

	custom := "https://example.test/v1internal:fetchAvailableModels"
	t.Setenv("AGM_QUOTA_URL", " "+custom+" ")
	if got := QuotaEndpoint(); got != custom {
		t.Fatalf("expected override %q, got %q", custom, got)
	}
}

func TestFetchLiveQuotaWithEndpoints_SuccessAndRounding(t *testing.T) {
	var seenProjectBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loadCodeAssist":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cloudaicompanionProject": "proj-123",
			})
		case "/fetchAvailableModels":
			_ = json.NewDecoder(r.Body).Decode(&seenProjectBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": map[string]any{
					"gemini-2.5-flash": map[string]any{
						"quotaInfo": map[string]any{
							"remainingFraction": 0.6380,
							"resetTime":         "2026-11-21T00:05:25Z",
						},
					},
					"claude-opus-5-5-high": map[string]any{
						"quotaInfo": map[string]any{
							"remainingFraction": 0.9346,
							"resetTime":         "2026-11-21T02:05:40Z",
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	q, err := fetchLiveQuotaWithEndpoints("tok", srv.URL+"/loadCodeAssist", srv.URL+"/fetchAvailableModels")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenProjectBody["project"] != "proj-123" {
		t.Fatalf("expected project proj-123, got %v", seenProjectBody["project"])
	}
	if got := q.Models["gemini-2.5-flash"].Percentage; got != 64 {
		t.Fatalf("expected rounded 64, got %d", got)
	}
	if got := q.Models["claude-opus-5-5-high"].Percentage; got != 93 {
		t.Fatalf("expected rounded 93, got %d", got)
	}
}

func TestFetchLiveQuotaWithEndpoints_PropagatesProjectErrors(t *testing.T) {
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer errSrv.Close()

	if _, err := fetchLiveQuotaWithEndpoints("tok", errSrv.URL, errSrv.URL); err == nil || !strings.Contains(err.Error(), "fetch project id") {
		t.Fatalf("expected fetch project id error, got %v", err)
	}

	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"cloudaicompanionProject": ""})
	}))
	defer emptySrv.Close()

	if _, err := fetchLiveQuotaWithEndpoints("tok", emptySrv.URL, emptySrv.URL); err == nil || !strings.Contains(err.Error(), "empty cloudaicompanionProject") {
		t.Fatalf("expected empty cloudaicompanionProject error, got %v", err)
	}
}
