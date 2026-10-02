package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

// TestMaybeRecallExperienceOnFailure covers the decision-time half of
// experience recall (r379, arXiv:2602.06052 "consolidation at decision
// time"): a matching past case surfaces at the failure moment, the gate is
// one-shot per run, cold/missing stores and unrelated errors stay silent.
func TestMaybeRecallExperienceOnFailure(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewExperienceStore(filepath.Join(dir, ".ggcode", "memory", "experience"))
	if _, _, err := store.Record("Fix the flaky login test",
		"Injected a clock into SessionValidator; the test asserted wall-time.",
		"success", []string{"internal/auth/session.go"}); err != nil {
		t.Fatalf("seed case failed: %v", err)
	}

	a := &Agent{workingDir: dir}

	// Matching failure: task + error text hit the seeded case.
	got := a.maybeRecallExperienceOnFailure("fix login test",
		"login test failed: SessionValidator asserted wall time, flaky under load: exit status 1")
	if got == "" {
		t.Fatal("expected decision-time recall for matching failure")
	}
	if !strings.Contains(got, "Past Experience for This Failure") || !strings.Contains(got, "clock") {
		t.Fatalf("recall block missing context or case detail: %q", got)
	}

	// One-shot: second trigger in the same run must stay silent.
	if again := a.maybeRecallExperienceOnFailure("fix login test", "login test failed again"); again != "" {
		t.Fatalf("gate must fire once per run, got %q", again)
	}

	// Unrelated error on a fresh agent: nothing relevant, nothing injected.
	b := &Agent{workingDir: dir}
	if got := b.maybeRecallExperienceOnFailure("write release notes", "spelling mistake in changelog"); got != "" {
		t.Fatalf("irrelevant failure should skip injection, got %q", got)
	}
	// Gate must NOT latch when nothing matched (the agent can still earn a
	// recall later in the run if a matching failure appears).
	if b.experienceFailureRecallFired {
		t.Fatal("no-match recall must not consume the one-shot gate")
	}

	// No workingDir: safe no-op.
	if got := (&Agent{}).maybeRecallExperienceOnFailure("anything", "boom"); got != "" {
		t.Fatalf("missing workingDir should recall nothing, got %q", got)
	}
}
