package handlers

import (
	"strings"
	"testing"
)

func TestSessionIndexCountsAreAbsolute(t *testing.T) {
	for _, q := range []string{sessionIndexCountCQL, sessionIndexEventCountCQL} {
		if strings.Contains(q, "+") {
			t.Fatalf("counter arithmetic is invalid on plain int columns: %s", q)
		}
	}
	if !strings.Contains(sessionIndexCountCQL, "event_count = ?") || !strings.Contains(sessionIndexCountCQL, "turn_count = ?") {
		t.Fatalf("expected absolute assignments: %s", sessionIndexCountCQL)
	}
	if eventCountFromSeq(1, 1) != 1 || eventCountFromSeq(1, 3) != 3 || eventCountFromSeq(4, 2) != 5 {
		t.Fatalf("seq counts %d %d %d", eventCountFromSeq(1, 1), eventCountFromSeq(1, 3), eventCountFromSeq(4, 2))
	}
	if countDistinctTurnIDs([]string{"t1", "t1", "t2", ""}) != 2 {
		t.Fatal(countDistinctTurnIDs([]string{"t1", "t1", "t2", ""}))
	}
}

func TestCreateSessionIndexCQLIsIdempotent(t *testing.T) {
	if !strings.Contains(createSessionIndexCQL, "IF NOT EXISTS") {
		t.Fatalf("create must be idempotent so a re-create does not reset live counters: %s", createSessionIndexCQL)
	}
	if !strings.Contains(createSessionIndexCQL, "global_sessions.session_index") {
		t.Fatalf("create must target global_sessions.session_index: %s", createSessionIndexCQL)
	}
}

func TestSessionLookupDoesNotUseLike(t *testing.T) {
	for _, q := range []string{sessionScopeByIDCQL, sessionByPrimaryKeyCQL} {
		if strings.Contains(strings.ToUpper(q), "LIKE") {
			t.Fatalf("LIKE is rejected on session_index.scope_id: %s", q)
		}
	}
	if !strings.Contains(sessionByPrimaryKeyCQL, "WHERE scope_id = ? AND session_id = ?") {
		t.Fatal(sessionByPrimaryKeyCQL)
	}
}
