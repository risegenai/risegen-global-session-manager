// Package handlers provides the HTTP API surface for the Global Session Manager.
package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/risegenai/risegen-global-session-manager/internal/metrics"
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
	mux.HandleFunc("GET /metrics", r.handleMetrics)

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
	cassandraStatus := "connected"
	httpStatus := http.StatusOK

	if err := r.Cassandra.Query("SELECT now() FROM system.local").Exec(); err != nil {
		cassandraStatus = "disconnected"
		httpStatus = http.StatusServiceUnavailable
		r.Logger.Error("healthz: cassandra unreachable", "error", err)
	}

	writeJSON(w, httpStatus, map[string]any{
		"ok":        cassandraStatus == "connected",
		"cassandra": cassandraStatus,
	})
}

func (r *Router) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "gsm_sessions_created_total %d\n", metrics.Global.SessionsCreated.Load())
	fmt.Fprintf(w, "gsm_events_appended_total %d\n", metrics.Global.EventsAppended.Load())
	fmt.Fprintf(w, "gsm_publications_received_total %d\n", metrics.Global.PublicationsRecv.Load())
	fmt.Fprintf(w, "gsm_publications_rejected_total %d\n", metrics.Global.PublicationsRej.Load())
	fmt.Fprintf(w, "gsm_fanout_calls_total %d\n", metrics.Global.FanoutCalls.Load())
	fmt.Fprintf(w, "gsm_fanout_errors_total %d\n", metrics.Global.FanoutErrors.Load())
	fmt.Fprintf(w, "gsm_rejected_inbound_total %d\n", metrics.Global.RejectedInbound.Load())
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

	metrics.Global.SessionsCreated.Add(1)

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

	metrics.Global.EventsAppended.Add(int64(n))

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
// Aggregation
// ---------------------------------------------------------------------------

func (r *Router) handleAggregatedSessions(w http.ResponseWriter, req *http.Request) {
	accountID := req.URL.Query().Get("account_id")
	scope := req.URL.Query().Get("scope")
	status := req.URL.Query().Get("status")

	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_account_id"})
		return
	}

	// Find all scopes for this account.
	var scopeIDs []string
	iter := r.Cassandra.Query(
		`SELECT scope_id FROM global_sessions.scope_registry WHERE account_id = ? ALLOW FILTERING`,
		accountID,
	).Iter()
	var sid string
	for iter.Scan(&sid) {
		scopeIDs = append(scopeIDs, sid)
	}
	_ = iter.Close()

	var allSessions []map[string]any
	for _, sc := range scopeIDs {
		if scope != "" && sc != scope {
			continue
		}
		var query string
		var args []any
		if status != "" {
			query = `SELECT scope_id, session_id, title, created_at, updated_at, turn_count, event_count, status
			 FROM global_sessions.session_index WHERE scope_id = ? AND status = ?`
			args = []any{sc, status}
		} else {
			query = `SELECT scope_id, session_id, title, created_at, updated_at, turn_count, event_count, status
			 FROM global_sessions.session_index WHERE scope_id = ?`
			args = []any{sc}
		}
		sessIter := r.Cassandra.Query(query, args...).Iter()
		var ssID, title, st string
		var createdAt, updatedAt time.Time
		var turnCount, eventCount int
		for sessIter.Scan(&ssID, nil, &title, &createdAt, &updatedAt, &turnCount, &eventCount, &st) {
			allSessions = append(allSessions, map[string]any{
				"scope_id":    sc,
				"session_id":  ssID,
				"title":       title,
				"created_at":  createdAt,
				"updated_at":  updatedAt,
				"turn_count":  turnCount,
				"event_count": eventCount,
				"status":      st,
			})
		}
		_ = sessIter.Close()
	}

	if allSessions == nil {
		allSessions = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": allSessions, "count": len(allSessions)})
}

func (r *Router) handleAggregatedSession(w http.ResponseWriter, req *http.Request) {
	scopeID := req.PathValue("scope_id")
	sessionID := req.PathValue("session_id")
	if scopeID == "" || sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_scope_id_or_session_id"})
		return
	}

	var title, status string
	var createdAt, updatedAt time.Time
	var turnCount, eventCount int
	err := r.Cassandra.Query(
		`SELECT title, created_at, updated_at, turn_count, event_count, status
		 FROM global_sessions.session_index WHERE scope_id = ? AND session_id = ?`,
		scopeID, sessionID,
	).Scan(&title, &createdAt, &updatedAt, &turnCount, &eventCount, &status)
	if err == gocql.ErrNotFound {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session_not_found"})
		return
	}
	if err != nil {
		r.Logger.Error("aggregated session", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_read_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope_id":    scopeID,
		"session_id":  sessionID,
		"title":       title,
		"created_at":  createdAt,
		"updated_at":  updatedAt,
		"turn_count":  turnCount,
		"event_count": eventCount,
		"status":      status,
	})
}

// ---------------------------------------------------------------------------
// Publication inbound
// ---------------------------------------------------------------------------

func (r *Router) handlePublication(w http.ResponseWriter, req *http.Request) {
	metrics.Global.PublicationsRecv.Add(1)

	var env struct {
		ScopeID            string    `json:"scope_id"`
		SessionID          string    `json:"session_id"`
		Title              string    `json:"title"`
		CreatedAt          time.Time `json:"created_at"`
		UpdatedAt          time.Time `json:"updated_at"`
		TurnCount          int       `json:"turn_count"`
		EventCount         int       `json:"event_count"`
		ContentDigest      string    `json:"content_digest"`
		Status             string    `json:"status"`
		BaseServiceVersion int       `json:"base_service_version"`
	}
	if err := json.NewDecoder(req.Body).Decode(&env); err != nil {
		metrics.Global.PublicationsRej.Add(1)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if env.ScopeID == "" || env.SessionID == "" {
		metrics.Global.PublicationsRej.Add(1)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_scope_id_or_session_id"})
		return
	}

	// Validate scope exists and is active.
	var scopeStatus string
	var registeredVersion int
	err := r.Cassandra.Query(
		`SELECT status, base_service_version FROM global_sessions.scope_registry WHERE scope_id = ?`,
		env.ScopeID,
	).Scan(&scopeStatus, &registeredVersion)
	if err == gocql.ErrNotFound {
		metrics.Global.PublicationsRej.Add(1)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "scope_not_found"})
		return
	}
	if err != nil {
		r.Logger.Error("publication: scope lookup", "error", err)
		metrics.Global.PublicationsRej.Add(1)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_read_failed"})
		return
	}
	if scopeStatus != "active" {
		metrics.Global.PublicationsRej.Add(1)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "scope_inactive"})
		return
	}

	// Check version compatibility (GSM version is 1).
	gsmVersion := 1
	pubVersion := env.BaseServiceVersion
	if pubVersion == 0 {
		pubVersion = registeredVersion
	}
	if pubVersion > gsmVersion {
		// Incompatible — reject to rejected_inbound.
		payload, _ := json.Marshal(env)
		r.Cassandra.Query(
			`INSERT INTO global_sessions.rejected_inbound (scope_id, ts, event_id, reason, payload)
			 VALUES (?, ?, ?, ?, ?)`,
			env.ScopeID, time.Now().UTC(), uuid.New().String(),
			fmt.Sprintf("incompatible_version: publisher=%d gsm=%d", pubVersion, gsmVersion),
			string(payload),
		).Exec()
		metrics.Global.PublicationsRej.Add(1)
		metrics.Global.RejectedInbound.Add(1)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "incompatible_version"})
		return
	}

	// Upsert into session_index.
	sid, err := uuid.Parse(env.SessionID)
	if err != nil {
		metrics.Global.PublicationsRej.Add(1)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_session_id"})
		return
	}
	now := time.Now().UTC()
	if env.CreatedAt.IsZero() {
		env.CreatedAt = now
	}
	if env.UpdatedAt.IsZero() {
		env.UpdatedAt = now
	}
	if env.Status == "" {
		env.Status = "active"
	}

	err = r.Cassandra.Query(
		`INSERT INTO global_sessions.session_index
		 (scope_id, session_id, title, created_at, updated_at, turn_count, event_count, content_digest, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		env.ScopeID, sid, env.Title, env.CreatedAt, env.UpdatedAt,
		env.TurnCount, env.EventCount, env.ContentDigest, env.Status,
	).Exec()
	if err != nil {
		r.Logger.Error("publication: upsert session_index", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cassandra_write_failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"scope_id": env.ScopeID, "session_id": env.SessionID, "status": "published"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}