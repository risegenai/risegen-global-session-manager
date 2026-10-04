package base

import "testing"

func TestVersionCompatibility(t *testing.T) {
	tests := []struct {
		name         string
		publisherVer int
		consumerVer  int
		want         CompatResult
	}{
		{"same version", 1, 1, Compatible},
		{"publisher behind, upgrade required", 1, 2, UpgradeRequired},
		{"publisher ahead, incompatible", 2, 1, Incompatible},
		{"same major, different minor (both v1)", 1, 1, Compatible},
		{"publisher v1, consumer v3", 1, 3, UpgradeRequired},
		{"publisher v3, consumer v1", 3, 1, Incompatible},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VersionCompatibility(tt.publisherVer, tt.consumerVer)
			if got != tt.want {
				t.Errorf("VersionCompatibility(%d, %d) = %v, want %v",
					tt.publisherVer, tt.consumerVer, got, tt.want)
			}
		})
	}
}

func TestValidEventType(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"session.created", true},
		{"message.user", true},
		{"turn.started", true},
		{"orchestrator.plan_issued", true},
		{"hosting_mode.activated", true},
		{"simple", true},
		{"with_underscore", true},
		{"", false},
		{"UPPERCASE", false},
		{"has space", false},
		{"two.dots.here", false},
		{"-dash", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidEventType(tt.name)
			if got != tt.valid {
				t.Errorf("ValidEventType(%q) = %v, want %v", tt.name, got, tt.valid)
			}
		})
	}
}

func TestScopeSessionEnvelope(t *testing.T) {
	env := ScopeSessionEnvelope{
		ScopeID:       "nexus:acc-1:inst-1",
		SessionID:     "sess-1",
		Title:         "test session",
		TurnCount:     3,
		EventCount:    12,
		ContentDigest: "sha256:abc123",
		Status:        StatusActive,
	}
	if env.ScopeID != "nexus:acc-1:inst-1" {
		t.Errorf("ScopeID = %q, want %q", env.ScopeID, "nexus:acc-1:inst-1")
	}
	if env.Status != StatusActive {
		t.Errorf("Status = %q, want %q", env.Status, StatusActive)
	}
}