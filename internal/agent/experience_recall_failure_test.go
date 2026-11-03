package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

func TestMaybeRecallExperienceOnFailure(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewExperienceStore(filepath.Join(dir, ".ggcode", "memory", "experience"))
	if _, _, err := store.Record("Fix the flaky login test",
		"Injected a clock into SessionValidator; the test asserted wall-time.",
		"success", []string{"internal/auth/session.go"}); err != nil {
		t.Fatalf("seed case failed: %v", err)
	}

	a := &Agent{workingDir: dir}

	got := a.maybeRecallExperienceOnFailure("fix login test",
		"login test failed: SessionValidator asserted wall time, flaky under load: exit status 1")
	if got == "" {
		t.Fatal("expected decision-time recall for matching failure")
	}
	if !strings.Contains(got, "Possibly Related Past Experience") || !strings.Contains(got, "clock") {
		t.Fatalf("recall block missing context or case detail: %q", got)
	}

	if again := a.maybeRecallExperienceOnFailure("fix login test", "login test failed again"); again != "" {
		t.Fatalf("gate must fire once per run, got %q", again)
	}

	b := &Agent{workingDir: dir}
	if got := b.maybeRecallExperienceOnFailure("write release notes", "spelling mistake in changelog"); got != "" {
		t.Fatalf("irrelevant failure should skip injection, got %q", got)
	}

	if b.experienceFailureRecallFired {
		t.Fatal("no-match recall must not consume the one-shot gate")
	}

	if got := (&Agent{}).maybeRecallExperienceOnFailure("anything", "boom"); got != "" {
		t.Fatalf("missing workingDir should recall nothing, got %q", got)
	}
}
