package handlers

// createSessionIndexCQL inserts a new session index row. IF NOT EXISTS keeps a
// re-create idempotent: channels re-post the create when they restart, and a
// plain INSERT would reset the live counters (turn_count/event_count) of a
// session that is still being appended to.
const createSessionIndexCQL = `INSERT INTO global_sessions.session_index
		 (scope_id, session_id, title, created_at, updated_at, turn_count, event_count, content_digest, status)
		 VALUES (?, ?, ?, ?, ?, 0, 0, '', 'active') IF NOT EXISTS`

const sessionScopeByIDCQL = `SELECT scope_id FROM global_sessions.session_index WHERE session_id = ? ALLOW FILTERING`

const sessionByPrimaryKeyCQL = `SELECT title, created_at, updated_at, turn_count, event_count, content_digest, status FROM global_sessions.session_index WHERE scope_id = ? AND session_id = ?`

// sessionIndexCountCQL assigns absolute counter columns. Cassandra rejects
// event_count = event_count + n on these plain int columns.
const sessionIndexCountCQL = `UPDATE global_sessions.session_index
		 SET event_count = ?, turn_count = ?, updated_at = ?
		 WHERE scope_id = ? AND session_id = ?`

// sessionIndexEventCountCQL updates only the event total when the distinct
// turn-id read failed. It must not reset turn_count.
const sessionIndexEventCountCQL = `UPDATE global_sessions.session_index
		 SET event_count = ?, updated_at = ?
		 WHERE scope_id = ? AND session_id = ?`

func eventCountFromSeq(firstSeq int64, n int) int {
	if n <= 0 {
		return int(firstSeq - 1)
	}
	return int(firstSeq + int64(n) - 1)
}

func countDistinctTurnIDs(ids []string) int {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		seen[id] = struct{}{}
	}
	return len(seen)
}
