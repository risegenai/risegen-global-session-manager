// Package handlers — permission decisions for the live remote-session
// channel (T048).
//
// When a client answers a session/request_permission frame, GSM records the
// answer as a PermissionDecision keyed by (session_id, request_id). The
// FIRST decision wins; later ones are rejected as already_decided and never
// overwrite it. A client disconnecting mid-permission leaves the request
// pending: nothing is recorded until a decision arrives.
package handlers

// permissionDecisionKey identifies one permission request inside one live
// session.
type permissionDecisionKey struct {
	sessionID string
	requestID string
}

// recordPermissionDecision records a permission decision for a
// (session_id, request_id) pair. The first decision wins and is kept; any
// later decision for the same pair returns alreadyDecided=true and does NOT
// overwrite the stored one.
func (r *Router) recordPermissionDecision(sessionID, requestID, decision string) (alreadyDecided bool) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	key := permissionDecisionKey{sessionID: sessionID, requestID: requestID}
	if _, ok := r.livePermissionDecisions[key]; ok {
		return true
	}
	r.livePermissionDecisions[key] = decision
	return false
}

// permissionDecision returns the winning decision for a
// (session_id, request_id) pair, or ok=false while the request is still
// pending.
func (r *Router) permissionDecision(sessionID, requestID string) (decision string, ok bool) {
	r.liveMu.Lock()
	defer r.liveMu.Unlock()
	decision, ok = r.livePermissionDecisions[permissionDecisionKey{sessionID: sessionID, requestID: requestID}]
	return decision, ok
}
