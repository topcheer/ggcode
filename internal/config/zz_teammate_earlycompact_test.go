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

// TestDefaultSwarmTeammateEarlyCompactRatio pins the 0.70 default preset by
// DefaultConfig (the base Load unmarshals over), that an explicit
// swarm.teammate_early_compact_ratio: 0 overrides it (0 = disable), and
// that an unrelated swarm section leaves the default untouched.
func TestDefaultSwarmTeammateEarlyCompactRatio(t *testing.T) {
	if got := DefaultConfig().Swarm.TeammateEarlyCompactRatio; got != 0.70 {
		t.Fatalf("DefaultConfig swarm.teammate_early_compact_ratio = %v, want 0.70", got)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte("swarm:\n  teammate_early_compact_ratio: 0\n"), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Swarm.TeammateEarlyCompactRatio != 0 {
		t.Fatalf("explicit 0 must override the 0.70 default (disable), got %v", cfg.Swarm.TeammateEarlyCompactRatio)
	}

	cfg2 := DefaultConfig()
	if err := yaml.Unmarshal([]byte("swarm:\n  inbox_size: 16\n"), cfg2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg2.Swarm.TeammateEarlyCompactRatio != 0.70 {
		t.Fatalf("unset key must keep the 0.70 default, got %v", cfg2.Swarm.TeammateEarlyCompactRatio)
	}
}
