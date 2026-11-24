// Package handlers — live session list (T048).
//
// GET /v1/live/sessions?machine=… lists the caller's live sessions: only
// sessions carried by daemons registered under the bearer token's account.
package handlers

import (
	"net/http"
	"sort"
)

// liveSessionEntry is one row of the sessions list response.
type liveSessionEntry struct {
	SessionID string `json:"session_id"`
	MachineID string `json:"machine_id"`
}

// handleLiveSessionsList lists the live sessions owned by the bearer token's
// account. An optional ?machine= filter restricts the list to one machine.
func (r *Router) handleLiveSessionsList(w http.ResponseWriter, req *http.Request) {
	tokenAccount := bearerAccount(req)
	if tokenAccount == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
		return
	}

	machine := req.URL.Query().Get("machine")

	r.liveMu.Lock()
	sessions := make([]liveSessionEntry, 0, len(r.liveSessions))
	for sessionID, daemon := range r.liveSessions {
		if daemon.account != tokenAccount {
			continue
		}
		if machine != "" && daemon.machineID != machine {
			continue
		}
		sessions = append(sessions, liveSessionEntry{
			SessionID: sessionID,
			MachineID: daemon.machineID,
		})
	}
	r.liveMu.Unlock()

	sort.Slice(sessions, func(i, j int) bool { return sessions[i].SessionID < sessions[j].SessionID })

	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}
