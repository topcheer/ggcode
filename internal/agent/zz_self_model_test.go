package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfModelPersistReloadMerge pins the durable self-card lifecycle
// (#feat-self-model P1): record -> save -> reload in a "new session"
// -> merged accumulation (SEAA: no static reset).
func TestSelfModelPersistReloadMerge(t *testing.T) {
	dir := t.TempDir()
	path := SelfModelPath(dir)

	m := LoadSelfModel(path)
	m.Record(path, "grep", false, "invalid pattern")
	m.Record(path, "grep", true, "")
	m.Record(path, "edit_file", true, "")

	// Reload as a fresh session would.
	m2 := LoadSelfModel(path)
	st := m2.Skills["grep"]
	if st == nil || st.Success != 1 || st.Fail != 1 {
		t.Fatalf("reload must keep grep stats, got %+v", st)
	}
	if st.LastFail != "invalid pattern" {
		t.Fatalf("last-fail excerpt must persist, got %q", st.LastFail)
	}
	// Merge semantics: further records accumulate.
	m2.Record(path, "grep", true, "")
	m3 := LoadSelfModel(path)
	if m3.Skills["grep"].Success != 2 {
		t.Fatalf("merge must accumulate across sessions, got %+v", m3.Skills["grep"])
	}
}

// TestSelfModelLimitCap pins the declared-limits ring (20 cap, oldest
// evicted) fed from capability-boundary escalations.
func TestSelfModelLimitCap(t *testing.T) {
	dir := t.TempDir()
	path := SelfModelPath(dir)
	m := LoadSelfModel(path)
	for i := 0; i < 25; i++ {
		m.AddLimit(path, "pivot limit "+string(rune('a'+i%26)))
	}
	if len(m.DeclaredLimits) != 20 {
		t.Fatalf("limits must cap at 20, got %d", len(m.DeclaredLimits))
	}
	if m.DeclaredLimits[len(m.DeclaredLimits)-1] == "pivot limit a" {
		t.Fatal("oldest limit must be evicted")
	}
}

// TestSelfModelDelegationContext pins the delegation card: top strengths
// only after >=3 tries, limits included, empty when no signal.
func TestSelfModelDelegationContext(t *testing.T) {
	dir := t.TempDir()
	path := SelfModelPath(dir)
	m := LoadSelfModel(path)
	if got := m.DelegationContext(); got != "" {
		t.Fatalf("empty model must render empty card, got %q", got)
	}
	for i := 0; i < 9; i++ {
		m.Record(path, "grep", true, "")
	}
	for i := 0; i < 4; i++ {
		m.Record(path, "browser", false, "js wall")
	}
	m.AddLimit(path, "approach-pivot x3 on frontend work")
	card := m.DelegationContext()
	if !strings.Contains(card, "grep 100%") {
		t.Fatalf("card must rank strengths, got %q", card)
	}
	if !strings.Contains(card, "known limits") || !strings.Contains(card, "approach-pivot") {
		t.Fatalf("card must carry declared limits, got %q", card)
	}
	if !strings.Contains(card, "browser") {
		t.Fatalf("weak skill must still surface, got %q", card)
	}
}

// TestSelfModelFileContract pins path + permissions contract.
func TestSelfModelFileContract(t *testing.T) {
	dir := t.TempDir()
	path := SelfModelPath(dir)
	m := LoadSelfModel(path)
	m.Record(path, "run_command", false, "boom")
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "self_model.json")); err != nil {
		t.Fatalf("self_model.json must exist under .ggcode: %v", err)
	}
}
