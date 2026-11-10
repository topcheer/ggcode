package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func newGatesTestAccess(t *testing.T) *configAccess {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	writeConfig(t, cfgPath, "")
	return newTestAccess(t, cfgPath)
}

func TestGatesSectionDefaults(t *testing.T) {
	access := newGatesTestAccess(t)
	for key, want := range map[string]string{
		"gates.merge.enabled":       "true",
		"gates.merge.commands":      `["gh pr merge","gh pr close"]`,
		"gates.merge.hold_patterns": "null",
		"gates.merge.preflight_ttl": "1m0s",
	} {
		got, err := access.Get(key)
		if err != nil {
			t.Fatalf("Get(%s): %v", key, err)
		}
		if got != want {
			t.Errorf("Get(%s) = %q, want %q", key, got, want)
		}
	}
	listed, err := access.List("gates")
	if err != nil || !strings.Contains(listed, "gates.merge") {
		t.Errorf("List(gates) = %q, %v", listed, err)
	}
}

func TestGatesSetAndPersistRoundtrip(t *testing.T) {
	access := newGatesTestAccess(t)

	if err := access.Set("gates.merge.enabled", "false"); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if err := access.Set("gates.merge.preflight_ttl", "90s"); err != nil {
		t.Fatalf("set ttl: %v", err)
	}
	if err := access.Set("gates.merge.hold_patterns", `["HOLD: awaiting owner","^do not merge$"]`); err != nil {
		t.Fatalf("set hold_patterns: %v", err)
	}
	if err := access.Set("gates.merge.commands", `["gh pr merge"]`); err != nil {
		t.Fatalf("set commands: %v", err)
	}

	// In-memory effect is immediate.
	if got, _ := access.Get("gates.merge.enabled"); got != "false" {
		t.Errorf("enabled = %q", got)
	}
	if got, _ := access.Get("gates.merge.preflight_ttl"); got != "1m30s" {
		t.Errorf("ttl = %q", got)
	}
	pats, err := access.gatesHoldPatterns()
	if err != nil || len(pats) != 2 {
		t.Fatalf("compiled hold patterns = %v, %v", pats, err)
	}

	// Persisted to disk and reloads intact.
	reloaded, err := config.Load(access.cfg.FilePath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	m := reloaded.Gates.Merge
	if m.EffectiveEnabled() {
		t.Error("enabled=false must persist")
	}
	if m.EffectivePreflightTTL() != 90*time.Second {
		t.Errorf("ttl = %s", m.EffectivePreflightTTL())
	}
	if len(m.HoldPatterns) != 2 || m.HoldPatterns[0] != "HOLD: awaiting owner" {
		t.Errorf("hold_patterns = %v", m.HoldPatterns)
	}
	if len(m.EffectiveCommands()) != 1 {
		t.Errorf("commands = %v", m.EffectiveCommands())
	}
}

func TestGatesSetRejectsBadValues(t *testing.T) {
	access := newGatesTestAccess(t)
	cases := []struct{ key, value, wantErr string }{
		{"gates.merge.enabled", "maybe", "invalid gates.merge.enabled"},
		{"gates.merge.hold_patterns", `["ok","["]`, "hold_patterns[1]"},
		{"gates.merge.commands", `["gh pr merge",""]`, "commands[1]"},
		{"gates.merge.preflight_ttl", "10ms", "preflight_ttl"},
		{"gates.merge.preflight_ttl", "2h", "preflight_ttl"},
		{"gates.merge.preflight_ttl", "banana", "invalid gates.merge.preflight_ttl"},
	}
	for _, tc := range cases {
		err := access.Set(tc.key, tc.value)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("Set(%s, %s) err = %v, want containing %q", tc.key, tc.value, err, tc.wantErr)
		}
	}
	// Rejected hold_patterns must not destroy previously valid patterns.
	if err := access.Set("gates.merge.hold_patterns", `["valid"]`); err != nil {
		t.Fatalf("seed valid patterns: %v", err)
	}
	if err := access.Set("gates.merge.hold_patterns", `["valid","["]`); err == nil {
		t.Fatal("bad pattern must be rejected")
	}
	if got, _ := access.Get("gates.merge.hold_patterns"); got != `["valid"]` {
		t.Errorf("rejected write must roll back, got %s", got)
	}
}

func TestGatesUnsetSemantics(t *testing.T) {
	access := newGatesTestAccess(t)
	if err := access.Set("gates.merge.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	if err := access.Set("gates.merge.hold_patterns", `["HOLD: awaiting owner","^do not merge$"]`); err != nil {
		t.Fatal(err)
	}
	if err := access.Set("gates.merge.preflight_ttl", "90s"); err != nil {
		t.Fatal(err)
	}

	adapter := gatesAccessAdapter{inner: access}

	// Scalar resets to default.
	if err := adapter.Unset("enabled", ""); err != nil {
		t.Fatalf("unset enabled: %v", err)
	}
	if got, _ := access.Get("gates.merge.enabled"); got != "true" {
		t.Errorf("unset enabled must restore default, got %q", got)
	}
	if err := adapter.Unset("preflight_ttl", ""); err != nil {
		t.Fatalf("unset ttl: %v", err)
	}
	if got, _ := access.Get("gates.merge.preflight_ttl"); got != "1m0s" {
		t.Errorf("unset ttl must restore default, got %q", got)
	}

	// List entries are removed by value.
	if err := adapter.Unset("hold_patterns", "HOLD: awaiting owner"); err != nil {
		t.Fatalf("unset list entry: %v", err)
	}
	if got, _ := access.Get("gates.merge.hold_patterns"); got != `["^do not merge$"]` {
		t.Errorf("unset by value must remove exactly one entry, got %s", got)
	}
	if err := adapter.Unset("hold_patterns", ""); err == nil {
		t.Error("list unset without a value must be rejected")
	}
	if err := adapter.Unset("hold_patterns", "not-present"); err == nil {
		t.Error("removing a missing entry must be rejected")
	}
}

func TestGatesDescribeAction(t *testing.T) {
	access := newGatesTestAccess(t)
	adapter := gatesAccessAdapter{inner: access}
	out, err := adapter.Describe()
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	for _, want := range []string{
		"\"enabled\": true",
		"\"commands\": [",
		"\"preflight_ttl\": \"1m0s\"",
		"defaults_in_use",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("describe missing %q:\n%s", want, out)
		}
	}
}

func TestGatesTestActionDryRun(t *testing.T) {
	access := newGatesTestAccess(t)
	adapter := gatesAccessAdapter{inner: access}

	out, err := adapter.Test("gh pr merge 5356")
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	for _, want := range []string{
		"protected: yes",
		"pr: 5356",
		// No live agent session in unit tests: snapshot state is honestly
		// unknown rather than guessed.
		"preflight snapshot: unknown (no live agent session",
		"dry-run only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("test output missing %q:\n%s", want, out)
		}
	}

	// Non-protected command reports no match.
	out, _ = adapter.Test("git push origin main")
	if !strings.Contains(out, "protected: no") {
		t.Errorf("non-protected must report no: %s", out)
	}

	// Disabled gate reports no protection.
	if err := access.Set("gates.merge.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	out, _ = adapter.Test("gh pr merge 5356")
	if !strings.Contains(out, "protected: no (gate disabled)") {
		t.Errorf("disabled gate must report no: %s", out)
	}
}

func TestGatesToolWritesPersistAtomically(t *testing.T) {
	access := newGatesTestAccess(t)
	adapter := gatesAccessAdapter{inner: access}
	if err := adapter.Set("preflight_ttl", `"2m"`); err != nil {
		t.Fatalf("tool set: %v", err)
	}
	data, err := os.ReadFile(access.cfg.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "preflight_ttl: 2m0s") {
		t.Errorf("config file must contain the persisted TTL:\n%s", data)
	}
}
