package plugin

import (
	"testing"
	"time"
)

// Cooldown re-probe must keep attempting recovery after the fast backoff
// exhausts — the old behavior left tools permanently unregistered (status
// Failed, no further attempts) for the rest of the session.
func TestMCPReconnectCooldownValue(t *testing.T) {
	if mcpReconnectCooldown < 30*time.Second {
		t.Fatalf("cooldown too aggressive: %s (would hammer a dead server)", mcpReconnectCooldown)
	}
	if mcpReconnectCooldown > 5*time.Minute {
		t.Fatalf("cooldown too lax: %s (server outage would outlast a session)", mcpReconnectCooldown)
	}
}
