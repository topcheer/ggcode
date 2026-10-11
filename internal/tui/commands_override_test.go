package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// r16: the manual override channel resolves a user query to exactly one
// recorded tag. Suppression is a state change, so unlike the drill-down
// (which tolerates picking the last of multiple substring hits) an
// ambiguous match must refuse and report candidates.
func TestResolveUniqueGuidanceTag(t *testing.T) {
	m := newTestModel()
	dir := t.TempDir()
	hints := filepath.Join(dir, ".ggcode", "memory", "guidance-hints.jsonl")
	body := `{"ts":"2026-10-09T10:00:00Z","tag":"Attention Fragmentation","text":"x"}
{"ts":"2026-10-09T11:00:00Z","tag":"ACT NOW: verify","text":"y"}
{"ts":"2026-10-09T12:00:00Z","tag":"Attention Drain","text":"z"}
`
	if err := os.MkdirAll(filepath.Dir(hints), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hints, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	// Exact (case-insensitive) match.
	if tag, ok := m.resolveUniqueGuidanceTag(dir, "attention fragmentation"); !ok || tag != "Attention Fragmentation" {
		t.Errorf("exact match failed: %q %v", tag, ok)
	}
	// Unique substring match resolves.
	if tag, ok := m.resolveUniqueGuidanceTag(dir, "verify"); !ok || tag != "ACT NOW: verify" {
		t.Errorf("unique substring failed: %q %v", tag, ok)
	}
	// "##" prefix is stripped like the drill-down does.
	if tag, ok := m.resolveUniqueGuidanceTag(dir, "## ACT NOW: verify"); !ok || tag != "ACT NOW: verify" {
		t.Errorf("prefix strip failed: %q %v", tag, ok)
	}
	// Ambiguous substring: ("", true) means refusal - candidates were
	// already emitted via chatWriteSystem.
	if tag, ok := m.resolveUniqueGuidanceTag(dir, "Attention"); !ok || tag != "" {
		t.Errorf("ambiguity must refuse with (\"\", true), got (%q, %v)", tag, ok)
	}
	// No match: ("", false).
	if tag, ok := m.resolveUniqueGuidanceTag(dir, "nonexistent"); ok || tag != "" {
		t.Errorf("no-match must be (\"\", false), got %q ok=%v", tag, ok)
	}
}

// isAllDigits must classify "suppress"/"reset" as non-numeric so the verb
// words route to the override branch, never the aggregate count branch.
func TestOverrideVerbsAreNonNumeric(t *testing.T) {
	for _, v := range []string{"suppress", "reset", "Suppress"} {
		if isAllDigits(v) {
			t.Errorf("%q must not be digits", v)
		}
	}
	if !isAllDigits("200") {
		t.Error("200 must be digits")
	}
}

// The hints file doubles as the tag registry; the miss message must list
// what is actually recorded.
func TestKnownGuidanceTags(t *testing.T) {
	m := newTestModel()
	dir := t.TempDir()
	hints := filepath.Join(dir, ".ggcode", "memory", "guidance-hints.jsonl")
	if err := os.MkdirAll(filepath.Dir(hints), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hints, []byte(`{"tag":"B","text":"y"}
{"tag":"A","text":"x"}
{"tag":"A","text":"x2"}
`), 0644); err != nil {
		t.Fatal(err)
	}
	got := m.knownGuidanceTags(dir)
	if !strings.Contains(got, "A") || !strings.Contains(got, "B") || strings.Count(got, "A") != 1 {
		t.Errorf("expected deduped sorted tag list, got %q", got)
	}
}
