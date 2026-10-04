package base

import "context"

// MetadataPublisher is the contract that per-Nexus Session Managers implement
// to push session metadata to the Global Session Manager. It is the interface
// the base service defines; the GSM provides the HTTP handler that accepts
// these publications, and each Nexus provides the client that sends them.
type MetadataPublisher interface {
	// PublishSession sends a session metadata envelope to the GSM.
	// Implementations must be at-least-once and idempotent — the GSM
	// deduplicates by (scope_id, session_id).
	PublishSession(ctx context.Context, env ScopeSessionEnvelope) error

	// PublishSessionArchived signals that a session has been archived.
	PublishSessionArchived(ctx context.Context, scopeID, sessionID string) error
}