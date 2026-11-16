package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regression: typing 2+ chars after an @ mention in mid-text triggered a
// synchronous, unbounded recursive WalkDir inside the Bubble Tea event loop,
// freezing the whole TUI (no input, Ctrl-C unresponsive) on large trees such
// as the user's home directory. recursiveFuzzySearch must respect its budget
// and return early.
func TestRecursiveFuzzySearchRespectsBudget(t *testing.T) {
	root := t.TempDir()
	// Build a tree with 200 files spread over 20 dirs; query matches nothing,
	// so an unbounded walk would visit every entry.
	for d := 0; d < 20; d++ {
		dir := filepath.Join(root, "d"+string(rune('a'+d)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 10; f++ {
			name := filepath.Join(dir, "file"+string(rune('0'+f))+".txt")
			if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Tiny budget: 10 visits max. An unbounded walk would see 220+ entries.
	got := recursiveFuzzySearchBudgeted(root, "zzznomatch", 4, 30, time.Hour, 10)
	if len(got) != 0 {
		t.Fatalf("expected no matches, got %v", got)
	}

	// Budget search must return promptly even with a generous visit cap but
	// zero time budget: proves the deadline guard terminates the walk.
	start := time.Now()
	_ = recursiveFuzzySearchBudgeted(root, "zzznomatch", 4, 30, 0, 1<<30)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("zero-budget walk did not terminate early: %v", elapsed)
	}

	// Sanity: matches are still found with a normal budget.
	got = recursiveFuzzySearchBudgeted(root, "file1", 4, 30, time.Hour, 1<<30)
	if len(got) == 0 {
		t.Fatal("expected fuzzy matches, got none")
	}

	// And the production entry point still works end to end.
	if got := recursiveFuzzySearch(root, "file2", 4, 30); len(got) == 0 {
		t.Fatal("recursiveFuzzySearch returned no matches")
	}
}
