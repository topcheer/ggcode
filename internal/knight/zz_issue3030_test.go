package knight

// Regression probe for #3030: nightly self-reflection entries are written
// unconditionally (MetaLesson never empty) and the eval memory window did
// not filter by kind - after ~8 nights the window became 100% statistics
// lines and real promotion/reject lessons were pushed out of the evaluator
// prompt. Self-reflection is now rate-limited to the most recent 1.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue3030_EvalWindowRateLimitsSelfReflection(t *testing.T) {
	projDir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), projDir, nil)

	// 7 nights of self-reflection, newest first ordering comes from Recent().
	for i := 0; i < 7; i++ {
		if err := k.RecordSemanticMemory("self-reflection",
			"active=3 staging=1 proposals=2 (night "+strings.Repeat("a", i)+")", nil, "nightly"); err != nil {
			t.Fatal(err)
		}
	}
	// One real lesson buried under the statistics pile.
	if err := k.RecordSemanticMemory("lesson", "reject-promote: cross-module edits need verify-ci not go build", nil, "eval"); err != nil {
		t.Fatal(err)
	}

	out := k.formatRecentSemanticMemoryForEval(8)
	if out == "" {
		t.Fatal("expected rendered memory")
	}
	selfCount := strings.Count(out, "[self-reflection]")
	if selfCount > 1 {
		t.Fatalf("#3030: eval window must keep at most 1 self-reflection line, got %d:\n%s", selfCount, out)
	}
	if !strings.Contains(out, "[lesson]") || !strings.Contains(out, "cross-module edits") {
		t.Fatalf("#3030: real lesson must stay visible past the self-reflection pile:\n%s", out)
	}
	if lines := strings.Count(out, "\n") + 1; lines > 8 {
		t.Fatalf("window must respect the limit, got %d lines", lines)
	}
}

func TestIssue3030_PureLessonWindowUnchanged(t *testing.T) {
	projDir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), projDir, nil)
	// #r25: semantic dedup merges near-identical recurrences into one entry,
	// so genuinely DISTINCT lessons are required here to keep the original
	// contract meaningful (five different lessons all render).
	lessons := []string{
		"reject-promote: cross-module edits need verify-ci not go build",
		"promote: flutter changes need widget tests before approval",
		"parser edge cases belong in table-driven tests",
		"guard concurrent map access with a path-keyed mutex",
		"prefer configuration over hardcoded constants",
	}
	for _, text := range lessons {
		if err := k.RecordSemanticMemory("lesson", text, nil, "eval"); err != nil {
			t.Fatal(err)
		}
	}
	out := k.formatRecentSemanticMemoryForEval(8)
	if lines := strings.Count(out, "\n") + 1; lines != len(lessons) {
		t.Fatalf("%d real lessons must all render, got %d lines:\n%s", len(lessons), lines, out)
	}
	if strings.Contains(out, "self-reflection") {
		t.Fatal("no self-reflection was written; none should appear")
	}
}

// TestIssue3030_WideWindowReachesOldRealLessons: even when 20+ self-
// reflection nights bury older lessons, the wider fetch window (limit*4)
// still surfaces them.
func TestIssue3030_WideWindowReachesOldRealLessons(t *testing.T) {
	projDir := t.TempDir()
	k := New(config.KnightConfig{Enabled: true}, t.TempDir(), projDir, nil)
	// Real lessons first (oldest), then a deep pile of nightly statistics.
	if err := k.RecordSemanticMemory("lesson", "promote: flutter changes need widget tests", nil, "eval"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if err := k.RecordSemanticMemory("self-reflection", "active=1 staging=0 proposals=9", nil, "nightly"); err != nil {
			t.Fatal(err)
		}
	}
	out := k.formatRecentSemanticMemoryForEval(8)
	if !strings.Contains(out, "widget tests") {
		t.Fatalf("#3030: buried real lesson must be reachable through the self-reflection pile:\n%s", out)
	}
}

var _ = filepath.Join // silence unused-import churn if the probe evolves
