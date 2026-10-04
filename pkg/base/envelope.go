// Package base defines the versioned base-service contract that the Global
// Session Manager and every per-Nexus session store import. It is published
// with each Nexus release and follows semver. Readers ignore unknown fields;
// unknown kind values are preserved as kind=unknown with bytes intact.
package base

import "time"

// ScopeSessionEnvelope is the metadata payload a per-Nexus Session Manager
// publishes to the GSM on session lifecycle events. It carries enough
// information for the GSM to index and aggregate without copying content.
type ScopeSessionEnvelope struct {
	ScopeID       string    `json:"scope_id"`
	SessionID     string    `json:"session_id"`
	Title         string    `json:"title"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	TurnCount     int       `json:"turn_count"`
	EventCount    int       `json:"event_count"`
	ContentDigest string    `json:"content_digest"`
	Status        string    `json:"status"`
}

// Session status constants.
const (
	StatusActive   = "active"
	StatusArchived = "archived"
	StatusOrphaned = "orphaned"
)

// Scope kind constants for scope_id prefix.
const (
	ScopeGlobal = "global"
	ScopeNexus  = "nexus"
)