package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSwarmTeammateEarlyCompactRatioYAMLKey pins the YAML key for the
// teammate early pre-compact ratio knob (swarm.teammate_early_compact_ratio).
func TestSwarmTeammateEarlyCompactRatioYAMLKey(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("swarm:\n  teammate_early_compact_ratio: 0.55\n"), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Swarm.TeammateEarlyCompactRatio != 0.55 {
		t.Fatalf("swarm.teammate_early_compact_ratio = %v, want 0.55", c.Swarm.TeammateEarlyCompactRatio)
	}
}
