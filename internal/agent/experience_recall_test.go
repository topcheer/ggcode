package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

// TestRecallExperienceMatchesAndSkips covers the recall side of the
// Experience Case Bank: relevant cases surface as a formatted block,
// irrelevant or empty stores yield "" (the skip-injection signal), and a
// workingDir-less agent is a safe no-op.
func TestRecallExperienceMatchesAndSkips(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewExperienceStore(filepath.Join(dir, ".ggcode", "memory", "experience"))
	if _, _, err := store.Record("Fix the flaky login test", "Injected a clock into SessionValidator.", "success",
		[]string{"internal/auth/session.go"}); err != nil {
		t.Fatalf("seed case failed: %v", err)
	}

	a := &Agent{workingDir: dir}
	idx := a.recallExperience("login test is flaky again in auth")
	if idx == "" {
		t.Fatalf("expected recall for matching task")
	}
	if !strings.Contains(idx, "outcome: success") || !strings.Contains(idx, "clock") {
		t.Fatalf("recall block missing case details: %q", idx)
	}

	// Unrelated query: nothing relevant, nothing injected.
	if got := a.recallExperience("write release notes for v2"); got != "" {
		t.Fatalf("irrelevant query should skip injection, got %q", got)
	}

	// No workingDir: safe no-op.
	if got := (&Agent{}).recallExperience("anything"); got != "" {
		t.Fatalf("missing workingDir should recall nothing, got %q", got)
	}
}
