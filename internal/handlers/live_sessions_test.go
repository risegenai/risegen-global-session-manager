package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLiveSessionsListOnlyOwnAccount: the sessions list endpoint must return
// only the sessions of daemons registered under the bearer token's account.
func TestLiveSessionsListOnlyOwnAccount(t *testing.T) {
	r := NewRouterWithLogger(testLogger())

	// Two daemons, each carrying one session, under different accounts.
	daemonA := &liveDaemon{machineID: "machine-a", account: "acct-a"}
	daemonB := &liveDaemon{machineID: "machine-b", account: "acct-b"}
	r.trackLiveSession(daemonA, "session-a")
	r.trackLiveSession(daemonB, "session-b")

	mux := NewRouter(r)

	req := httptest.NewRequest(http.MethodGet, "/v1/live/sessions", nil)
	req.Header.Set("Authorization", "Bearer acct-a:secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/live/sessions: status = %d, want %d (body %s)", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
			MachineID string `json:"machine_id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
	}

	if len(body.Sessions) != 1 {
		t.Fatalf("len(sessions) = %d, want 1 (body %s)", len(body.Sessions), rec.Body.String())
	}
	got := body.Sessions[0]
	if got.SessionID != "session-a" {
		t.Errorf("session_id = %q, want %q", got.SessionID, "session-a")
	}
	if got.MachineID != "machine-a" {
		t.Errorf("machine_id = %q, want %q", got.MachineID, "machine-a")
	}
	for _, s := range body.Sessions {
		if s.SessionID == "session-b" {
			t.Errorf("foreign account's session leaked into list: %q", s.SessionID)
		}
	}
}
