package knight

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

	mem, mErr := k.RecentSemanticMemory(5)
	if mErr != nil {
		return
	}
	for _, m := range mem {
		if strings.Contains(m.Summary, "active=0") {
			t.Fatalf("broken index must not be persisted as an active=0 lesson: %q", m.Summary)
		}
	}
}

func TestIssue3638_HealthyIndexStillSucceeds(t *testing.T) {
	dir := t.TempDir()

	k := &Knight{projDir: dir, index: NewSkillIndex(filepath.Join(dir, "missing-home"), filepath.Join(dir, ".ggcode", "skills"))}
	report, err := k.RunSelfReflection(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("healthy index must not error after #3638 fix: %v", err)
	}
	if report.ActiveSkills != 0 || report.StagingSkills != 0 {
		t.Fatalf("expected empty index, got active=%d staging=%d", report.ActiveSkills, report.StagingSkills)
	}
}
