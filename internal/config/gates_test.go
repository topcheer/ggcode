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
		// #5401: the five bypass forms are gated now. Selector-less and
		// branch-selector forms hit with PR 0 (agent must identify the PR).
		{"gh pr merge", 0, true},                                                 // no selector: current branch's PR
		{"gh pr merge --auto --squash 5356", 5356, true},                         // flags before positional
		{"gh pr merge https://github.com/topcheer/ggcode/pull/5356", 5356, true}, // URL selector
		{"gh pr merge feature-branch", 0, true},                                  // branch selector: gated, PR 0
		{"gh pr close --delete-branch 5356", 5356, true},
		{"gh pr merge abc", 0, true},          // non-numeric selector = branch form
		{"gh pr merge -d --merge 9", 9, true}, // short flags skipped
		{"git push origin main", 0, false},    // not a protected pattern
		{"gh pr merge 0", 0, true},            // explicit 0 is not a PR; still gated
		{"echo gh pr merge 5356", 0, false},   // prefix-anchored: echo is not gh
		{"", 0, false},
		{"gh pr merge -1", 0, true}, // -1 is a flag-shaped token; gated, PR 0
		// URL with trailing path/query junk still extracts the tail number
		{"gh pr merge https://github.com/o/r/pull/77/files?diff=unified", 77, true},
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

// TestMatchMergeGateCompoundCommands pins #5407: compound commands gate on
// the unit that actually runs the merge/close action, quote-aware so
// separators inside string arguments do not split units, and echo units
// (plain or in a compound) never false-positive.
// TestMatchMergeGateEnvPrefix pins #5411: leading VAR=value assignments
// prefix real gh invocations and must be skipped before prefix matching,
// while echo heads (with or without = in their args) never match.
func TestMatchMergeGateEnvPrefix(t *testing.T) {
	def := DefaultMergeGateCommands
	cases := []struct {
		cmd     string
		wantPR  int
		wantHit bool
	}{
		{"GH_TOKEN=x gh pr merge 5356", 5356, true},
		{"A=1 B=2 gh pr close 42", 42, true},                 // stacked assignments
		{"GH_TOKEN=x gh pr merge", 0, true},                  // env + selector-less
		{"cd /d && GH_TOKEN=x gh pr merge 5356", 5356, true}, // env inside compound unit
		{"echo FOO=1", 0, false},                             // = in an ARG, echo head
		{"echo gh pr merge 5356", 0, false},                  // classic echo head
		{"GH_TOKEN=x echo gh pr merge 5356", 0, false},       // env + echo head
		{"--flag=1 gh pr merge 5356", 0, false},              // flag-shaped = token is not stripped
	}
	for _, tc := range cases {
		pr, hit := MatchMergeGateCommand(tc.cmd, def)
		if hit != tc.wantHit || (hit && pr != tc.wantPR) {
			t.Errorf("MatchMergeGateCommand(%q) = %d,%v want %d,%v", tc.cmd, pr, hit, tc.wantPR, tc.wantHit)
		}
	}
}

func TestMatchMergeGateCompoundCommands(t *testing.T) {
	def := DefaultMergeGateCommands
	cases := []struct {
		cmd     string
		wantPR  int
		wantHit bool
	}{
		// acceptance: compound forms hit
		{"cd /repo/dir && gh pr merge 5356", 5356, true},
		{"git status; gh pr merge --auto", 0, true}, // ; separator, no selector
		{"make release && gh pr close 42 --delete-branch", 42, true},
		{"go build ./... | tee log; gh pr merge 8", 8, true},          // pipe + ; mix
		{"gh pr view 9 && gh pr merge 9 || echo keep-going", 9, true}, // || chain
		{"echo x\necho y\ngh pr merge 7", 7, true},                    // newline separated
		// acceptance: echo compound display forms do NOT hit
		{"echo gh pr merge 5356 && echo done", 0, false},
		{"echo \"gh pr merge 5356\"", 0, false},
		{"echo hello && echo gh pr merge 5356", 0, false},
		// acceptance: separators inside quoted args do not split units -
		// the whole echo (including the quoted separator) is one echo unit
		{"echo \"a && gh pr merge 5356\" && true", 0, false},
		// escaped separator stays inside the unit (no split, no match)
		{"echo a \\&\\& gh pr merge 5356", 0, false},
		// leading env assignment prefix stays non-matching (conservative)
		// #5411 flipped: an env assignment prefixes a REAL gh invocation, so
		// this is now gated (was the deliberate conservative side in #5407).
		{"FOO=1 gh pr merge 5356", 5356, true},
	}
	for _, tc := range cases {
		pr, hit := MatchMergeGateCommand(tc.cmd, def)
		if hit != tc.wantHit || (hit && pr != tc.wantPR) {
			t.Errorf("MatchMergeGateCommand(%q) = %d,%v want %d,%v", tc.cmd, pr, hit, tc.wantPR, tc.wantHit)
		}
	}
}

// TestMatchMergeGateWrapperPrefix pins #5418: env/sudo (and the #5347
// wrapper family) as COMMAND tokens prefix real gh invocations and must be
// stripped before matching, including env followed by further assignments.
func TestMatchMergeGateWrapperPrefix(t *testing.T) {
	def := DefaultMergeGateCommands
	cases := []struct {
		cmd     string
		wantPR  int
		wantHit bool
	}{
		// issue probe table: both bypass forms flip to gated
		{"env FOO=1 gh pr merge 5356", 5356, true},
		{"sudo gh pr merge 5356", 5356, true},
		{"nohup gh pr close 42", 42, true},
		{"xargs gh pr merge 5356", 5356, true},
		{"sudo env FOO=1 gh pr merge 5356", 5356, true}, // wrappers and assignments alternate
		{"cd /d && sudo gh pr merge 5356", 5356, true},  // wrapper inside a compound unit
		{"env gh pr merge", 0, true},                    // bare env + selector-less
		// echo heads stay ungated in every wrapper shape
		{"env echo gh pr merge 5356", 0, false},
		{"sudo echo FOO=1", 0, false},
		// timeout WITH an argument keeps the argument (selector-shaped
		// token, not a wrapper) - deliberately non-matching by design
		{"timeout 30 gh pr merge 5356", 0, false},
	}
	for _, tc := range cases {
		pr, hit := MatchMergeGateCommand(tc.cmd, def)
		if hit != tc.wantHit || (hit && pr != tc.wantPR) {
			t.Errorf("MatchMergeGateCommand(%q) = %d,%v want %d,%v", tc.cmd, pr, hit, tc.wantPR, tc.wantHit)
		}
	}
}
