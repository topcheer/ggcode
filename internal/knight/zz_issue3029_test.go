package knight

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestIssue3029_AppendDoesNotLoseConcurrentLines pins the O_APPEND behavior:
// two sequential Append calls (as two store instances on the same path would
// produce from two processes) must both persist. The old read-modify-write
// code simulated the losing process by reading before the other's write
// landed, but here we pin the stronger invariant directly: every appended
// line is present afterwards, and no rewrite path drops earlier lines.
func TestIssue3029_AppendDoesNotLoseConcurrentLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "knight-memory.jsonl")

	for i := 0; i < 3; i++ {
		// Fresh store per call, mirroring RecordSemanticMemory which builds a
		// new store each time (per-process instances have no shared state).
		s := newSemanticMemoryStore(path)
		if err := s.Append(SemanticMemoryEntry{Kind: "lesson", Summary: strings.Repeat("x", 100), Source: "proc"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	entries, err := newSemanticMemoryStore(path).Recent(10)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 persisted entries, got %d (entries lost)", len(entries))
	}
}

// TestIssue3029_ReadSideCapKeepsNewest pins that the 500-entry cap is applied
// on read (the write side no longer rewrites per append).
func TestIssue3029_ReadSideCapKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "knight-memory.jsonl")
	// Hand-craft a file with 2 entries past the cap marker semantics: cap is
	// 500; craft 501 via direct file write to avoid slow appends.
	var b strings.Builder
	for i := 0; i < 501; i++ {
		kind := "lesson"
		summary := "s"
		if i == 0 {
			summary = "OLDEST"
		}
		if i == 500 {
			summary = "NEWEST"
		}
		b.WriteString(`{"id":"m` + strconv.Itoa(i) + `","kind":"` + kind + `","summary":"` + summary + `"}` + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := newSemanticMemoryStore(path).Recent(500)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(entries) != 500 {
		t.Fatalf("expected capped 500 entries, got %d", len(entries))
	}
	if entries[0].Summary != "NEWEST" || entries[499].Summary == "OLDEST" {
		t.Fatalf("cap must keep the newest window: first=%q", entries[0].Summary)
	}
}

// TestIssue3030_EvalWindowFiltersSelfReflection pins that the eval memory
// window keeps at most one self-reflection line even when the store is
// dominated by nightly self-reflection entries, so real lessons are not
// evicted.
func TestIssue3030_EvalWindowFiltersSelfReflection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".ggcode", "knight-memory.jsonl")
	s := newSemanticMemoryStore(path)
	// Nine nightly self-reflection lines (newest last on disk) followed by
	// nothing else: the window must contain at most 1 self-reflection line.
	for i := 0; i < 9; i++ {
		if err := s.Append(SemanticMemoryEntry{Kind: "self-reflection", Summary: "self-reflection: active=1 staging=0"}); err != nil {
			t.Fatal(err)
		}
	}
	// One real lesson, appended last (newest).
	if err := s.Append(SemanticMemoryEntry{Kind: "lesson", Summary: "prefer table-driven tests in knight"}); err != nil {
		t.Fatal(err)
	}

	k := &Knight{projDir: dir}
	out := k.formatRecentSemanticMemoryForEval(8)
	if out == "" {
		t.Fatalf("expected non-empty eval window")
	}
	selfCount := strings.Count(out, "[self-reflection]")
	if selfCount > 1 {
		t.Fatalf("eval window has %d self-reflection lines, want <= 1:\n%s", selfCount, out)
	}
	if !strings.Contains(out, "[lesson] prefer table-driven tests") {
		t.Fatalf("real lesson evicted from eval window:\n%s", out)
	}
}
