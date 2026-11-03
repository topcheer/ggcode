package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
)

func seed3072Store(t *testing.T, dir string) {
	t.Helper()
	store := memory.NewExperienceStore(filepath.Join(dir, ".ggcode", "memory", "experience"))
	if _, _, err := store.Record("Fix the flaky login test",
		"Injected a clock into SessionValidator; the test asserted wall-time.",
		"success", []string{"internal/auth/session.go"}); err != nil {
		t.Fatalf("seed case 1: %v", err)
	}
}

func TestIssue3072_ConditionalWording(t *testing.T) {
	dir := t.TempDir()
	seed3072Store(t, dir)
	a := &Agent{workingDir: dir}
	got := a.maybeRecallExperienceOnFailure("fix login test",
		"login test failed: SessionValidator asserted wall time, flaky under load: exit status 1")
	if got == "" {
		t.Fatal("expected decision-time recall for matching failure")
	}
	if strings.Contains(got, "A similar failure pattern was seen in this project before.") {
		t.Fatal("unconditional failure-pattern assertion still present (V1)")
	}
	if !strings.Contains(got, "MAY") && !strings.Contains(got, "may") {
		t.Fatalf("conditional hedging missing from header: %q", got)
	}
	if !strings.Contains(got, "keyword") {
		t.Fatalf("match-basis disclosure missing from header: %q", got)
	}
}

func TestIssue3072_NoReinjectionOfRunStartCases(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewExperienceStore(filepath.Join(dir, ".ggcode", "memory", "experience"))
	if _, _, err := store.Record("Fix the flaky login test",
		"Injected a clock into SessionValidator; the test asserted wall-time.",
		"success", []string{"internal/auth/session.go"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := &Agent{workingDir: dir}

	if idx := a.recallExperience("fix login test"); idx == "" {
		t.Fatal("run-start recall found nothing")
	}
	if len(a.experienceInjectedCaseIDs) == 0 {
		t.Fatal("run-start recall did not track injected case IDs")
	}

	if got := a.maybeRecallExperienceOnFailure("fix login test",
		"login test failed: SessionValidator asserted wall time, flaky: exit status 1"); got != "" {
		t.Fatalf("run-start case re-injected at decision time (V2): %q", got)
	}
}

func TestIssue3072_ExclusionPicksFreshCase(t *testing.T) {
	dir := t.TempDir()
	store := memory.NewExperienceStore(filepath.Join(dir, ".ggcode", "memory", "experience"))
	if _, _, err := store.Record("Fix the flaky login test",
		"Injected a clock into SessionValidator; the test asserted wall-time.",
		"success", []string{"internal/auth/session.go"}); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	if _, _, err := store.Record("deadline exceeded in validator suite",
		"Bumped the SessionValidator deadline and parallelized the fixture.",
		"success", []string{"internal/auth/session.go"}); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	a := &Agent{workingDir: dir}
	if idx := a.recallExperience("fix login test"); idx == "" {
		t.Fatal("run-start recall found nothing")
	}
	got := a.maybeRecallExperienceOnFailure("fix login test",
		"login test failed: SessionValidator deadline asserted wall time: exit status 1")
	if got == "" {
		t.Fatal("expected the second case to surface after exclusion")
	}
	if strings.Contains(got, "Injected a clock") {
		t.Fatal("excluded run-start case still present in decision-time block (V2)")
	}
}

func TestIssue3072_TailBiasedTruncation(t *testing.T) {
	var b strings.Builder
	b.WriteString("=== RUN TestAll\n")
	for i := 0; i < 200; i++ {
		b.WriteString("package/internal/subpkg fixture line ")
		b.WriteString(strings.Repeat("x", 8))
		b.WriteString("\n")
	}
	b.WriteString("--- FAIL: TestAll: session.go:42: SessionValidator asserted wall time\n")
	long := b.String()

	got := truncateErrorForQuery(long)
	if !strings.Contains(got, "SessionValidator asserted wall time") {
		t.Fatal("tail error line lost by truncation (V3)")
	}
	if len([]rune(got)) > 80+160+16 {
		t.Fatalf("truncation budget exceeded: %d runes", len([]rune(got)))
	}

	short := "boom: exit status 1"
	if truncateErrorForQuery(short) != short {
		t.Fatalf("short error mutated: %q", truncateErrorForQuery(short))
	}
}
