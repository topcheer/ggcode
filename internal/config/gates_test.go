package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestMergeGateDefaults(t *testing.T) {
	var m MergeGateConfig
	if !m.EffectiveEnabled() {
		t.Error("zero-value merge gate must default to enabled")
	}
	cmds := m.EffectiveCommands()
	if len(cmds) != 2 || cmds[0] != "gh pr merge" || cmds[1] != "gh pr close" {
		t.Errorf("default commands = %v", cmds)
	}
	if m.EffectivePreflightTTL() != 60*time.Second {
		t.Errorf("default TTL = %s", m.EffectivePreflightTTL())
	}
	if pats := m.EffectiveHoldPatterns(); len(pats) != 0 {
		t.Errorf("default hold patterns = %v", pats)
	}
}

func TestMergeGateExplicitOverrides(t *testing.T) {
	f := false
	m := MergeGateConfig{
		Enabled:      &f,
		Commands:     []string{"gh pr merge"},
		HoldPatterns: []string{"HOLD: awaiting owner"},
		PreflightTTL: 2 * time.Minute,
	}
	if m.EffectiveEnabled() {
		t.Error("explicit disable must hold")
	}
	if got := m.EffectiveCommands(); len(got) != 1 || got[0] != "gh pr merge" {
		t.Errorf("commands = %v", got)
	}
	if m.EffectivePreflightTTL() != 2*time.Minute {
		t.Errorf("TTL = %s", m.EffectivePreflightTTL())
	}
	re, err := m.CompiledHoldPatterns()
	if err != nil || len(re) != 1 {
		t.Fatalf("CompiledHoldPatterns = %v, %v", re, err)
	}
}

func TestValidateMergeGateConfig(t *testing.T) {
	c := &Config{}
	c.Gates.Merge.HoldPatterns = []string{"ok", "["}
	if err := c.ValidateMergeGateConfig(); err == nil || !strings.Contains(err.Error(), `hold_patterns[1] "[`) {
		t.Errorf("bad regex must be rejected with entry named, got %v", err)
	}

	c = &Config{}
	c.Gates.Merge.Commands = []string{"gh pr merge", "  "}
	if err := c.ValidateMergeGateConfig(); err == nil || !strings.Contains(err.Error(), "commands[1]") {
		t.Errorf("empty command entry must be rejected, got %v", err)
	}

	c = &Config{}
	c.Gates.Merge.PreflightTTL = 500 * time.Millisecond
	if err := c.ValidateMergeGateConfig(); err == nil || !strings.Contains(err.Error(), "preflight_ttl") {
		t.Errorf("sub-second TTL must be rejected, got %v", err)
	}

	c = &Config{}
	c.Gates.Merge.PreflightTTL = time.Hour
	if err := c.ValidateMergeGateConfig(); err == nil {
		t.Error("oversized TTL must be rejected")
	}

	c = &Config{}
	c.Gates.Merge.PreflightTTL = 90 * time.Second
	if err := c.ValidateMergeGateConfig(); err != nil {
		t.Errorf("valid TTL rejected: %v", err)
	}
}

func TestMatchMergeGateCommand(t *testing.T) {
	def := DefaultMergeGateCommands
	cases := []struct {
		cmd     string
		wantPR  int
		wantHit bool
	}{
		{"gh pr merge 5356", 5356, true},
		{"gh pr merge 5356 --squash --delete-branch", 5356, true},
		{"gh pr close 42", 42, true},
		{"gh pr merged 5356", 0, false},
		{"gh pr merge", 0, true},           // bare merge IS gated (pr unresolvable)
		{"gh pr merge abc", 0, true},       // branch operand IS gated (pr unresolvable)
		{"git push origin main", 0, false}, // not a protected pattern
		{"gh pr merge 0", 0, true},         // malformed number still gates (pr unresolvable)
		{"echo gh pr merge 5356", 0, false},
		{"", 0, false},
		{"gh pr merge -1", 0, true}, // negative still gates (pr unresolvable)
		// #5401: flag-first / URL / branch / bare forms must not bypass the gate
		{"gh pr merge --auto --squash 5356", 5356, true},
		{"gh pr merge https://github.com/topcheer/ggcode/pull/5356", 5356, true},
		{"gh pr close --delete-branch 5356", 5356, true},
	}
	for _, tc := range cases {
		pr, hit := MatchMergeGateCommand(tc.cmd, def)
		if hit != tc.wantHit || (hit && pr != tc.wantPR) {
			t.Errorf("MatchMergeGateCommand(%q) = %d,%v want %d,%v", tc.cmd, pr, hit, tc.wantPR, tc.wantHit)
		}
	}

	// Custom patterns fully replace the defaults.
	pr, hit := MatchMergeGateCommand("gh pr merge 7", []string{"glab mr merge"})
	if hit || pr != 0 {
		t.Errorf("custom pattern set must not match gh commands: %d,%v", pr, hit)
	}
	pr, hit = MatchMergeGateCommand("glab mr merge 7", []string{"glab mr merge"})
	if !hit || pr != 7 {
		t.Errorf("custom pattern must match its own command: %d,%v", pr, hit)
	}
}

func TestGatesYAMLRoundtrip(t *testing.T) {
	in := `
gates:
  merge:
    enabled: false
    commands: ["gh pr merge", "gh pr edit"]
    hold_patterns: ["HOLD: awaiting owner", "^do not merge$"]
    preflight_ttl: 90s
`
	var c Config
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Gates.Merge.EffectiveEnabled() {
		t.Error("enabled=false must roundtrip")
	}
	if c.Gates.Merge.PreflightTTL != 90*time.Second {
		t.Errorf("preflight_ttl = %s", c.Gates.Merge.PreflightTTL)
	}
	if len(c.Gates.Merge.HoldPatterns) != 2 {
		t.Fatalf("hold_patterns = %v", c.Gates.Merge.HoldPatterns)
	}
	if err := c.ValidateMergeGateConfig(); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
	out, err := yaml.Marshal(&c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), "preflight_ttl: 1m30s") {
		t.Errorf("duration must remarshal as duration string: %s", out)
	}
	var c2 Config
	if err := yaml.Unmarshal(out, &c2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if c2.Gates.Merge.PreflightTTL != 90*time.Second {
		t.Errorf("roundtrip TTL = %s", c2.Gates.Merge.PreflightTTL)
	}
}
