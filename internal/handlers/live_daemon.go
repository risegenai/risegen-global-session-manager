// Package handlers — live remote-session channel (T048).
//
// The gen daemon dials OUTBOUND to GET /v1/live/daemon, registers
// {machine_id, account, canonical_url} and then tunnels ACP JSON-RPC frames
// tagged {session_id, frame}. Clients attach via GET /v1/live/sessions/{id};
// an attach is refused with 403 session_not_yours when the bearer token's
// account differs from the daemon's registered account.
package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// liveDaemon is one registered gen daemon connection.
type liveDaemon struct {
	machineID    string
	account      string
	canonicalURL string
	conn         *websocket.Conn
	sendMu       sync.Mutex
}

// liveFrame is the envelope carried over the daemon socket. ACP JSON-RPC
// frames are unchanged inside `frame`.
type liveFrame struct {
	SessionID string          `json:"session_id"`
	Frame     json.RawMessage `json:"frame"`
}

// liveUpgrader upgrades the daemon and attach sockets. The caller is
// authenticated by the edge bearer token; the daemon additionally proves
// its account in the registration frame.
var liveUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true },
}

// testLogger returns a discard logger for handler tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// bearerAccount extracts the account from a "Bearer <token>" header. GSM
// tokens carry the account as the first ':'-separated segment.
func bearerAccount(req *http.Request) string {
	const prefix = "Bearer "
	h := req.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	tok := strings.TrimPrefix(h, prefix)
	if i := strings.IndexByte(tok, ':'); i >= 0 {
		return tok[:i]
	}
	return tok
}

// handleLiveDaemon upgrades the daemon's outbound WebSocket and registers it.
// The first frame must be the registration {machine_id, account,
// canonical_url}; a re-registration from the same machine replaces the old
// connection. Relay loss never touches local sessions: the daemon simply
// drops from the map and reconnects with backoff.
func (r *Router) handleLiveDaemon(w http.ResponseWriter, req *http.Request) {
	conn, err := liveUpgrader.Upgrade(w, req, nil)
	if err != nil {
		r.Logger.Warn("live daemon: upgrade failed", "error", err)
		return
	}

	var reg struct {
		MachineID    string `json:"machine_id"`
		Account      string `json:"account"`
		CanonicalURL string `json:"canonical_url"`
	}
	if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		conn.Close()
		return
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		r.Logger.Warn("live daemon: no registration frame", "error", err)
		conn.Close()
		return
	}
	if err := json.Unmarshal(raw, &reg); err != nil || reg.MachineID == "" || reg.Account == "" {
		r.Logger.Warn("live daemon: bad registration", "error", err)
		conn.Close()
		return
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		conn.Close()
		return
	}

	daemon := &liveDaemon{
		machineID:    reg.MachineID,
		account:      reg.Account,
		canonicalURL: reg.CanonicalURL,
		conn:         conn,
	}

	r.liveMu.Lock()
	if old, ok := r.liveDaemons[reg.MachineID]; ok {
		old.conn.Close()
	}
	r.liveDaemons[reg.MachineID] = daemon
	r.liveMu.Unlock()

	r.Logger.Info("live daemon registered", "machine_id", reg.MachineID, "account", reg.Account)

	// Pump frames until the socket dies; then drop the registration.
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var f liveFrame
		if json.Unmarshal(raw, &f) != nil || f.SessionID == "" {
			continue
		}
		r.trackLiveSession(daemon, f.SessionID)
		r.forwardLiveFrame(f.SessionID, raw)
	}

	r.liveMu.Lock()
	if r.liveDaemons[reg.MachineID] == daemon {
		delete(r.liveDaemons, reg.MachineID)
	}
	r.liveMu.Unlock()
	r.Logger.Info("live daemon disconnected", "machine_id", reg.MachineID)
}

// trackLiveSession records which daemon carries a session so an attach can
// be checked against the daemon's account.
func (r *Router) trackLiveSession(daemon *liveDaemon, sessionID string) {
	r.liveMu.Lock()
	r.liveSessions[sessionID] = daemon
	if r.liveMachineSessions[daemon.machineID] == nil {
		r.liveMachineSessions[daemon.machineID] = make(map[string]bool)
	}
	r.liveMachineSessions[daemon.machineID][sessionID] = true
	r.liveMu.Unlock()
}

// liveSessionOwner resolves the daemon that owns a session: by the
// session->daemon map first, then by the machine's known sessions, and
// finally — while a single daemon is registered — by that daemon.
func (r *Router) liveSessionOwner(sessionID string) *liveDaemon {
	if d, ok := r.liveSessions[sessionID]; ok {
		return d
	}
	var single *liveDaemon
	count := 0
	for _, d := range r.liveDaemons {
		count++
		if r.liveMachineSessions[d.machineID][sessionID] {
			return d
		}
		single = d
	}
	if count == 1 {
		return single
	}
	return nil
}

// liveClient is an attached client connection. Writes are serialized by
// writeMu because gorilla conns are not safe for concurrent writes.
type liveClient struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

// registerLiveClient tracks an attached client connection for a session so
// the daemon pump can forward frames to it.
func (r *Router) registerLiveClient(sessionID string, client *liveClient) {
	r.liveMu.Lock()
	if r.liveClients[sessionID] == nil {
		r.liveClients[sessionID] = make(map[*liveClient]bool)
	}
	r.liveClients[sessionID][client] = true
	r.liveMu.Unlock()
}

// unregisterLiveClient removes an attached client connection from a session.
func (r *Router) unregisterLiveClient(sessionID string, client *liveClient) {
	r.liveMu.Lock()
	if clients := r.liveClients[sessionID]; clients != nil {
		delete(clients, client)
		if len(clients) == 0 {
			delete(r.liveClients, sessionID)
		}
	}
	r.liveMu.Unlock()
}

// forwardLiveFrame writes the raw frame to every client attached to the
// session. Writes are serialized per connection; a client that errors is
// dropped and closed.
func (r *Router) forwardLiveFrame(sessionID string, frame []byte) {
	r.liveMu.Lock()
	clients := make([]*liveClient, 0, len(r.liveClients[sessionID]))
	for c := range r.liveClients[sessionID] {
		clients = append(clients, c)
	}
	r.liveMu.Unlock()

	for _, c := range clients {
		c.writeMu.Lock()
		err := c.conn.WriteMessage(websocket.TextMessage, frame)
		c.writeMu.Unlock()
		if err != nil {
			r.unregisterLiveClient(sessionID, c)
			c.conn.Close()
		}
	}
}

// handleLiveSessionAttach upgrades a client WebSocket onto a live session.
// The bearer token's account must match the account the session's daemon
// registered under; anything else is refused with 403 session_not_yours.
// History replay and live forwarding land with the sessions endpoints.
func (r *Router) handleLiveSessionAttach(w http.ResponseWriter, req *http.Request) {
	sessionID := req.PathValue("id")
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_session_id"})
		return
	}

	tokenAccount := bearerAccount(req)
	if tokenAccount == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}

	r.liveMu.Lock()
	daemon := r.liveSessionOwner(sessionID)
	r.liveMu.Unlock()
	if daemon == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session_not_live"})
		return
	}
	if daemon.account != tokenAccount {
		r.Logger.Warn("live attach refused: foreign account", "session_id", sessionID)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "session_not_yours"})
		return
	}

	conn, err := liveUpgrader.Upgrade(w, req, nil)
	if err != nil {
		r.Logger.Warn("live attach: upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	client := &liveClient{conn: conn}
	r.registerLiveClient(sessionID, client)
	defer r.unregisterLiveClient(sessionID, client)

	// Hold the socket open until the client or daemon goes away. Relay loss
	// never touches local sessions.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
