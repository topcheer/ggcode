package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

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
	// P1 pointer contract (#5446): one-line index + recall_experience hint;
	// the full approach body ("clock") is no longer injected at run start.
	if !strings.Contains(idx, "[success]") || !strings.Contains(idx, "flaky login test") {
		t.Fatalf("recall pointer missing outcome/task gist: %q", idx)
	}
	if !strings.Contains(idx, "recall_experience") {
		t.Fatalf("recall pointer must instruct recall_experience: %q", idx)
	}
	if strings.Contains(idx, "clock") {
		t.Fatalf("pointer must not carry the full approach body: %q", idx)
	}

	if got := a.recallExperience("write release notes for v2"); got != "" {
		t.Fatalf("irrelevant query should skip injection, got %q", got)
	}

	if got := (&Agent{}).recallExperience("anything"); got != "" {
		t.Fatalf("missing workingDir should recall nothing, got %q", got)
	}
}
