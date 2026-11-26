package main

import (
	"os"
	"strings"
	"testing"
)

// TestComposePassesBinarySigningKeyEnv guards the compose/binary contract: the
// gsm service must set the SAME env var main.go reads for the JWT signing key
// (GLOBAL_JWT_SIGNING_KEY), not a dead name the binary ignores. Counter: exactly
// one signing-key env assignment in docker-compose.yml, and it is GLOBAL_.
// Before the fix the file set GSM_JWT_SIGNING_KEY (ignored) -> this test is RED.
func TestComposePassesBinarySigningKeyEnv(t *testing.T) {
	raw, err := os.ReadFile("../../docker-compose.yml")
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	global, dead := 0, 0
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "GLOBAL_JWT_SIGNING_KEY:") {
			global++
		}
		if strings.HasPrefix(trimmed, "GSM_JWT_SIGNING_KEY:") {
			dead++
		}
	}
	if dead != 0 {
		t.Errorf("compose sets dead GSM_JWT_SIGNING_KEY %d time(s); binary reads GLOBAL_JWT_SIGNING_KEY", dead)
	}
	if global != 1 {
		t.Errorf("compose GLOBAL_JWT_SIGNING_KEY assignments = %d, want 1", global)
	}
}