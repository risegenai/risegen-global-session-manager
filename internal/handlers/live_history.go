package handlers

import (
	"encoding/json"
	"time"

	"github.com/gocql/gocql"
)

// historyEvent is one durable session event from
// global_sessions.global_session_events, in replay order.
type historyEvent struct {
	Seq       int64
	EventID   string
	EventType string
	TurnID    string
	Payload   string
	TS        time.Time
}

// historyReaderForTest is a test seam for sessionHistory; nil in production.
var historyReaderForTest func(r *Router, sessionID string) ([]historyEvent, error)

// sessionHistory resolves the session's durable history for an attach
// replay. It prefers the test seam, then the Cassandra read path, and
// falls back to an empty history (live-only attach) when the service has
// no persistence.
func sessionHistory(r *Router, sessionID string) ([]historyEvent, error) {
	if historyReaderForTest != nil {
		return historyReaderForTest(r, sessionID)
	}
	if r.Cassandra == nil {
		return nil, nil
	}
	return r.readSessionHistory(sessionID)
}

// readSessionHistory resolves the session's scope_id and scans its durable
// events in seq ASC order, the same queries handleListEvents uses. An
// unknown session yields an empty history so the attach stays live-only.
func (r *Router) readSessionHistory(sessionID string) ([]historyEvent, error) {
	var scopeID string
	if err := r.Cassandra.Query(
		`SELECT scope_id FROM global_sessions.session_index WHERE session_id = ? ALLOW FILTERING`,
		sessionID,
	).Scan(&scopeID); err != nil {
		if err == gocql.ErrNotFound {
			return []historyEvent{}, nil
		}
		// A transient read failure must not masquerade as an empty history
		// (the handleAppendEvents convention): surface it so the attach logs
		// the cause and continues live-only.
		return nil, err
	}

	iter := r.Cassandra.Query(
		`SELECT seq, event_id, event_type, turn_id, payload, ts
		 FROM global_sessions.global_session_events
		 WHERE scope_id = ? AND session_id = ?
		 ORDER BY seq ASC`,
		scopeID, sessionID,
	).Iter()

	var events []historyEvent
	var ev historyEvent
	for iter.Scan(&ev.Seq, &ev.EventID, &ev.EventType, &ev.TurnID, &ev.Payload, &ev.TS) {
		events = append(events, ev)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return events, nil
}

// historyEnvelope builds the replay frame for one history event: the live
// envelope shape plus the history marker, seq, event_type and ts. The
// payload is embedded as a JSON object when it is one, else as a string.
func historyEnvelope(sessionID string, ev historyEvent) ([]byte, error) {
	frame := json.RawMessage(ev.Payload)
	if !isJSONObject(frame) {
		s, err := json.Marshal(ev.Payload)
		if err != nil {
			return nil, err
		}
		frame = s
	}
	return json.Marshal(map[string]any{
		"session_id": sessionID,
		"frame":      frame,
		"history":    true,
		"seq":        ev.Seq,
		"event_type": ev.EventType,
		"ts":         ev.TS,
	})
}

// isJSONObject reports whether raw is a valid JSON object.
func isJSONObject(raw json.RawMessage) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	_, ok := v.(map[string]any)
	return ok
}
