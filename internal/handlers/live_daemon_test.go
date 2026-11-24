package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLiveDaemonForeignAccount: a daemon registered under one account must
// not be attachable by a token of another account — 403 session_not_yours.
func TestLiveDaemonForeignAccount(t *testing.T) {
	r := NewRouterWithLogger(testLogger())

	daemon := &liveDaemon{
		machineID: "machine-a",
		account:   "acct-owner",
	}
	r.liveMu.Lock()
	r.liveDaemons["machine-a"] = daemon
	r.liveMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/v1/live/sessions/sess-1", nil)
	req.Header.Set("Authorization", "Bearer token:acct-foreign")
	rec := httptest.NewRecorder()

	// Route through the real mux so req.PathValue("id") resolves.
	NewRouter(r).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for foreign account, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["error"] != "session_not_yours" {
		t.Fatalf("expected error session_not_yours, got %q", body["error"])
	}
}
