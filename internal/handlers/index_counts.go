package handlers

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
