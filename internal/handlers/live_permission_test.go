package handlers

import "testing"

// TestPermissionDecisionFirstWins pins the contract: the first recorded
// permission decision for a (session_id, request_id) pair wins; any later
// decision for the same pair is rejected as already_decided and must not
// overwrite the first.
func TestPermissionDecisionFirstWins(t *testing.T) {
	r := NewRouterWithLogger(testLogger())

	const sessionID = "sess-1"
	const requestID = "req-1"

	if already := r.recordPermissionDecision(sessionID, requestID, "allow"); already {
		t.Fatal("first decision reported already_decided")
	}

	if already := r.recordPermissionDecision(sessionID, requestID, "deny"); !already {
		t.Fatal("second decision was accepted; first decision must win")
	}

	got, ok := r.permissionDecision(sessionID, requestID)
	if !ok {
		t.Fatal("first decision not found after double record")
	}
	if got != "allow" {
		t.Fatalf("stored decision = %q, want %q (first decision must stand)", got, "allow")
	}
}
