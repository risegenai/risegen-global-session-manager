// Package handlers provides the HTTP API surface for the Global Session Manager.
package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// Router holds the GSM's HTTP handler dependencies.
type Router struct {
	Cassandra *gocql.Session
	Logger    *slog.Logger
}

// NewRouter builds the GSM HTTP mux.
func NewRouter(r *Router) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", r.handleHealthz)

	// Global-scope sessions
	mux.HandleFunc("POST /v1/global/sessions", r.handleCreateGlobalSession)
	mux.HandleFunc("GET /v1/global/sessions/{id}", r.handleGetGlobalSession)
	mux.HandleFunc("POST /v1/global/sessions/{id}/events", r.handleAppendEvents)
	mux.HandleFunc("GET /v1/global/sessions/{id}/events", r.handleListEvents)

	// Scope registry
	mux.HandleFunc("POST /v1/scopes", r.handleRegisterScope)
	mux.HandleFunc("GET /v1/scopes/{scope_id}", r.handleGetScope)
	mux.HandleFunc("PUT /v1/scopes/{scope_id}/heartbeat", r.handleHeartbeat)

	// Aggregation
	mux.HandleFunc("GET /v1/aggregated/sessions", r.handleAggregatedSessions)
	mux.HandleFunc("GET /v1/aggregated/sessions/{scope_id}/{session_id}", r.handleAggregatedSession)

	// Publication inbound
	mux.HandleFunc("POST /v1/publication/session", r.handlePublication)

	return mux
}

func (r *Router) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Global-scope sessions
// ---------------------------------------------------------------------------

func (r *Router) handleCreateGlobalSession(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Service string `json:"service"`
		Title   string `json:"title"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if body.Service == "" {
		body.Service = "unknown"
	}

	scopeID := "global:" + body.Service
	sessionID := uuid.New()
	now := time.Now().UTC()

	err := r.Cassandra.Query(
		`INSERT INTO global_sessions.session_index
		 (scope_id, session_id, title, created_at, updated_at, turn_count, event_count, content_digest, status)
		 VALUES (?, ?, ?, ?, ?, 0, 0, '', 'active')`,
		scopeID, sessionID, body.Title, now, now,
	).Exec()
	if err != nil {
		r.Logger.Error("create session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_write_failed"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"scope_id":   scopeID,
		"session_id": sessionID.String(),
		"title":      body.Title,
		"created_at": now,
	})
}

func (r *Router) handleGetGlobalSession(w http.ResponseWriter, req *http.Request) {
	sessionID := req.PathValue("id")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_session_id"})
		return
	}

	// Query across all global scopes — in production this would be scoped
	// to the authenticated account's scopes.
	var scopeID, title, status string
	var createdAt, updatedAt time.Time
	var turnCount, eventCount int
	var contentDigest string

	err := r.Cassandra.Query(
		`SELECT scope_id, session_id, title, created_at, updated_at, turn_count, event_count, content_digest, status
		 FROM global_sessions.session_index WHERE scope_id LIKE 'global:%' AND session_id = ? ALLOW FILTERING`,
		sessionID,
	).Scan(&scopeID, nil, &title, &createdAt, &updatedAt, &turnCount, &eventCount, &contentDigest, &status)

	if err == gocql.ErrNotFound {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session_not_found"})
		return
	}
	if err != nil {
		r.Logger.Error("get session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_read_failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scope_id":       scopeID,
		"session_id":     sessionID,
		"title":          title,
		"created_at":     createdAt,
		"updated_at":     updatedAt,
		"turn_count":     turnCount,
		"event_count":    eventCount,
		"content_digest": contentDigest,
		"status":         status,
	})
}

func (r *Router) handleAppendEvents(w http.ResponseWriter, req *http.Request) {
	sessionID := req.PathValue("id")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_session_id"})
		return
	}

	var body struct {
		Events []struct {
			EventType string `json:"event_type"`
			TurnID    string `json:"turn_id"`
			Payload   string `json:"payload"`
		} `json:"events"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if len(body.Events) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty_events"})
		return
	}

	eventTypeShape := regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)?$`)
	for _, ev := range body.Events {
		if !eventTypeShape.MatchString(ev.EventType) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_event_type", "event_type": ev.EventType})
			return
		}
	}

	// Allocate seq atomically via LWT on a counter row.
	// The counter row key is (scope_id, session_id) — we need scope_id.
	// For global sessions, scope_id is derived from the session.
	var scopeID string
	err := r.Cassandra.Query(
		`SELECT scope_id FROM global_sessions.session_index WHERE session_id = ? ALLOW FILTERING`,
		sessionID,
	).Scan(&scopeID)
	if err != nil {
		r.Logger.Error("append events: resolve scope", "error", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session_not_found"})
		return
	}

	n := len(body.Events)
	var lastSeq int64
	applied, err := r.Cassandra.Query(
		`UPDATE global_sessions.global_session_events
		 SET seq = seq + ? WHERE scope_id = ? AND session_id = ?
		 IF EXISTS`,
		n, scopeID, sessionID,
	).ScanCAS(&lastSeq)
	if err != nil {
		r.Logger.Error("append events: seq allocation", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "seq_allocation_failed"})
		return
	}
	if !applied {
		// Counter row doesn't exist yet — initialize it.
		// Use a simpler approach: read max seq and increment.
		var maxSeq int64
		err := r.Cassandra.Query(
			`SELECT seq FROM global_sessions.global_session_events
			 WHERE scope_id = ? AND session_id = ?
			 ORDER BY seq DESC LIMIT 1`,
			scopeID, sessionID,
		).Scan(&maxSeq)
		if err != nil && err != gocql.ErrNotFound {
			r.Logger.Error("append events: read max seq", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "seq_read_failed"})
			return
		}
		lastSeq = maxSeq
	}

	firstSeq := lastSeq + 1
	now := time.Now().UTC()

	for i, ev := range body.Events {
		seq := firstSeq + int64(i)
		eventID := uuid.New()
		turnID := ev.TurnID
		if turnID == "" {
			turnID = uuid.New().String()
		}
		err := r.Cassandra.Query(
			`INSERT INTO global_sessions.global_session_events
			 (scope_id, session_id, seq, event_id, event_type, turn_id, payload, ts)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			scopeID, sessionID, seq, eventID, ev.EventType, turnID, ev.Payload, now,
		).Exec()
		if err != nil {
			r.Logger.Error("append events: insert", "error", err, "seq", seq)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "event_insert_failed"})
			return
		}
	}

	// Update session_index counters.
	r.Cassandra.Query(
		`UPDATE global_sessions.session_index
		 SET event_count = event_count + ?, turn_count = turn_count + 1, updated_at = ?
		 WHERE scope_id = ? AND session_id = ?`,
		n, now, scopeID, sessionID,
	).Exec()

	writeJSON(w, http.StatusOK, map[string]any{
		"first_seq": firstSeq,
		"last_seq":  firstSeq + int64(n) - 1,
		"count":     n,
	})
}

func (r *Router) handleListEvents(w http.ResponseWriter, req *http.Request) {
	sessionID := req.PathValue("id")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_session_id"})
		return
	}

	// Resolve scope_id first.
	var scopeID string
	err := r.Cassandra.Query(
		`SELECT scope_id FROM global_sessions.session_index WHERE session_id = ? ALLOW FILTERING`,
		sessionID,
	).Scan(&scopeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session_not_found"})
		return
	}

	iter := r.Cassandra.Query(
		`SELECT seq, event_id, event_type, turn_id, payload, ts
		 FROM global_sessions.global_session_events
		 WHERE scope_id = ? AND session_id = ?
		 ORDER BY seq ASC`,
		scopeID, sessionID,
	).Iter()

	var events []map[string]any
	var seq int64
	var eventID, eventType, turnID, payload string
	var ts time.Time

	for iter.Scan(&seq, &eventID, &eventType, &turnID, &payload, &ts) {
		events = append(events, map[string]any{
			"seq":        seq,
			"event_id":   eventID,
			"event_type": eventType,
			"turn_id":    turnID,
			"payload":    payload,
			"ts":         ts,
		})
	}
	if err := iter.Close(); err != nil {
		r.Logger.Error("list events", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_read_failed"})
		return
	}

	if events == nil {
		events = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// ---------------------------------------------------------------------------
// Scope registry
// ---------------------------------------------------------------------------

func (r *Router) handleRegisterScope(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ScopeID             string `json:"scope_id"`
		AccountID           string `json:"account_id"`
		NexusInstanceID     string `json:"nexus_instance_id"`
		BaseServiceVersion  int    `json:"base_service_version"`
		Endpoint            string `json:"endpoint"`
		CredentialID        string `json:"credential_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if body.ScopeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_scope_id"})
		return
	}

	accountID, _ := uuid.Parse(body.AccountID)
	nexusInstanceID, _ := uuid.Parse(body.NexusInstanceID)

	err := r.Cassandra.Query(
		`INSERT INTO global_sessions.scope_registry
		 (scope_id, account_id, nexus_instance_id, base_service_version, endpoint, credential_id, last_heartbeat, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'active')`,
		body.ScopeID, accountID, nexusInstanceID, body.BaseServiceVersion,
		body.Endpoint, body.CredentialID, time.Now().UTC(),
	).Exec()
	if err != nil {
		r.Logger.Error("register scope", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_write_failed"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"scope_id": body.ScopeID, "status": "active"})
}

func (r *Router) handleGetScope(w http.ResponseWriter, req *http.Request) {
	scopeID := req.PathValue("scope_id")
	if scopeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_scope_id"})
		return
	}

	var accountID, nexusInstanceID gocql.UUID
	var baseServiceVersion int
	var endpoint, credentialID, status string
	var lastHeartbeat time.Time

	err := r.Cassandra.Query(
		`SELECT account_id, nexus_instance_id, base_service_version, endpoint, credential_id, last_heartbeat, status
		 FROM global_sessions.scope_registry WHERE scope_id = ?`,
		scopeID,
	).Scan(&accountID, &nexusInstanceID, &baseServiceVersion, &endpoint, &credentialID, &lastHeartbeat, &status)

	if err == gocql.ErrNotFound {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "scope_not_found"})
		return
	}
	if err != nil {
		r.Logger.Error("get scope", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_read_failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scope_id":             scopeID,
		"account_id":           accountID.String(),
		"nexus_instance_id":    nexusInstanceID.String(),
		"base_service_version": baseServiceVersion,
		"endpoint":             endpoint,
		"credential_id":        credentialID,
		"last_heartbeat":       lastHeartbeat,
		"status":               status,
	})
}

func (r *Router) handleHeartbeat(w http.ResponseWriter, req *http.Request) {
	scopeID := req.PathValue("scope_id")
	if scopeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_scope_id"})
		return
	}

	err := r.Cassandra.Query(
		`UPDATE global_sessions.scope_registry
		 SET last_heartbeat = ?, status = 'active'
		 WHERE scope_id = ?`,
		time.Now().UTC(), scopeID,
	).Exec()
	if err != nil {
		r.Logger.Error("heartbeat", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_write_failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"scope_id": scopeID, "heartbeat": "ok"})
}

// ---------------------------------------------------------------------------
// Aggregation (stubs — full implementation in Wave 3)
// ---------------------------------------------------------------------------

func (r *Router) handleAggregatedSessions(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}, "note": "aggregation_wave_3"})
}

func (r *Router) handleAggregatedSession(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"note": "aggregation_wave_3"})
}

// ---------------------------------------------------------------------------
// Publication inbound (stub — full implementation in Wave 3)
// ---------------------------------------------------------------------------

func (r *Router) handlePublication(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]any{"note": "publication_wave_3"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}