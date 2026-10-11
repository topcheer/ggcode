package knight

// #3638 probes: RunSelfReflection must propagate skill-index read failures
// instead of swallowing them. Before the fix, a failed scan surfaced as
// "active=0 staging=0", the false conclusion was written to semantic memory
// as a meta-lesson (source=nightly-reflection), and every caller's error
// branch (scheduler.go nightly loop, TUI) was dead code - violating the
// #1262 discipline already enforced in governance.go.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIssue3638_SelfReflectionPropagatesIndexError(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".ggcode", "skills")
	k := &Knight{projDir: dir, index: NewSkillIndex(filepath.Join(dir, "missing-home"), skillsDir)}

	// Hard-fail the scan: occupy the skills dir with a regular file
	// (same injection as validator_issue985_test.go). A merely missing
	// directory scans as empty-success, which must NOT be conflated.
	if err := os.RemoveAll(skillsDir); err != nil {
		t.Fatalf("remove skills dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(skillsDir), 0o755); err != nil {
		t.Fatalf("ensure parent dir: %v", err)
	}
	if err := os.WriteFile(skillsDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("plant broken skills dir: %v", err)
	}

	_, err := k.RunSelfReflection(context.Background(), 0)
	if err == nil {
		t.Fatal("RunSelfReflection must fail when the skill index cannot be scanned - the swallowed error used to be persisted as a false \"active=0\" meta-lesson (#3638)")
	}
	if !strings.Contains(err.Error(), "active skills unavailable") {
		t.Errorf("error should name the active-skills read failure, got: %v", err)
	}

	// No false meta-lesson may reach semantic memory.
	mem, mErr := k.RecentSemanticMemory(5)
	if mErr != nil {
		return // memory store unavailable in this bare Knight: nothing recorded
	}
	for _, m := range mem {
		if strings.Contains(m.Summary, "active=0") {
			t.Fatalf("broken index must not be persisted as an active=0 lesson: %q", m.Summary)
		}
	}
}

func TestIssue3638_HealthyIndexStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	// Empty-but-existing layout scans fine; reflection must keep succeeding
	// and recording its (genuinely true) zeroed lesson.
	k := &Knight{projDir: dir, index: NewSkillIndex(filepath.Join(dir, "missing-home"), filepath.Join(dir, ".ggcode", "skills"))}
	report, err := k.RunSelfReflection(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("healthy index must not error after #3638 fix: %v", err)
	}
	if report.ActiveSkills != 0 || report.StagingSkills != 0 {
		t.Fatalf("expected empty index, got active=%d staging=%d", report.ActiveSkills, report.StagingSkills)
	}
}
