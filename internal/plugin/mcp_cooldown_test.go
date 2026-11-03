package plugin

import (
	"testing"
	"time"
)

func TestMCPReconnectCooldownValue(t *testing.T) {
	if mcpReconnectCooldown < 30*time.Second {
		t.Fatalf("cooldown too aggressive: %s (would hammer a dead server)", mcpReconnectCooldown)
	}
	if mcpReconnectCooldown > 5*time.Minute {
		t.Fatalf("cooldown too lax: %s (server outage would outlast a session)", mcpReconnectCooldown)
	}
}
