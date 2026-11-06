package knight

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #5114 strengthen: end-to-end cross-project regression, straight from the
// issue's trigger sequence — a global skill promoted in project A must be
// rollable back from a promoter bound to project B (Promote and Rollback read
// the same scope-domain snapshot dir), plus the scheduler startup dir.
// Complements zz_issue5114_test.go, which pins the helpers directly.

func writeSkillFile5114(t *testing.T, path, name, scope, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	fm := "---\nname: " + name + "\ndescription: " + body + "\nscope: " + scope + "\ncreated_by: knight\n---\n# " + body + "\n"
	if err := os.WriteFile(path, []byte(fm), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestIssue5114GlobalRollbackFromAnotherProject(t *testing.T) {
	home := t.TempDir()
	projA := t.TempDir()
	projB := t.TempDir()

	promoterA := NewPromoter(home, projA)

	activePath := filepath.Join(home, ".ggcode", "skills", "deploy", "SKILL.md")
	writeSkillFile5114(t, activePath, "deploy", "global", "v1-global-old")
	stagingPath := filepath.Join(home, ".ggcode", "skills-staging", "knight-20260101-deploy.md")
	writeSkillFile5114(t, stagingPath, "deploy", "global", "v2-global-new")

	if err := promoterA.Promote(&SkillEntry{Name: "deploy", Path: stagingPath, Scope: "global", Staging: true}); err != nil {
		t.Fatalf("Promote(global) in project A failed: %v", err)
	}

	// Snapshot must live in the home scope domain, not project A's tree.
	projASnaps, _ := os.ReadDir(filepath.Join(projA, ".ggcode", "skills-snapshots"))
	if len(projASnaps) != 0 {
		t.Fatalf("global promote must not snapshot into project A's dir, found %d", len(projASnaps))
	}
	homeSnaps, _ := os.ReadDir(filepath.Join(home, ".ggcode", "skills-snapshots"))
	if len(homeSnaps) != 1 {
		t.Fatalf("global promote must snapshot into home dir, found %d", len(homeSnaps))
	}

	// Project B session: a fresh promoter bound to projB must see the global
	// snapshots taken in project A and roll the global skill back.
	promoterB := NewPromoter(home, projB)
	if err := promoterB.Rollback(&SkillEntry{Name: "deploy", Path: activePath, Scope: "global"}); err != nil {
		t.Fatalf("global rollback from project B must work post-#5114: %v", err)
	}

	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "v1-global-old") {
		t.Fatalf("rollback must restore the snapshotted old global version, got:\n%s", string(data))
	}
}

func TestIssue5114ProjectSnapshotsStayProjectLocal(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	p := NewPromoter(home, proj)

	activePath := filepath.Join(proj, ".ggcode", "skills", "build-flow", "SKILL.md")
	writeSkillFile5114(t, activePath, "build-flow", "project", "v1-proj-old")
	stagingPath := filepath.Join(proj, ".ggcode", "skills-staging", "knight-20260101-build-flow.md")
	writeSkillFile5114(t, stagingPath, "build-flow", "project", "v2-proj-new")

	if err := p.Promote(&SkillEntry{Name: "build-flow", Path: stagingPath, Scope: "project", Staging: true}); err != nil {
		t.Fatalf("Promote(project) failed: %v", err)
	}

	projSnaps, _ := os.ReadDir(filepath.Join(proj, ".ggcode", "skills-snapshots"))
	if len(projSnaps) != 1 {
		t.Fatalf("project promote must keep its snapshot in the project dir, found %d", len(projSnaps))
	}
	if homeSnaps, err := os.ReadDir(filepath.Join(home, ".ggcode", "skills-snapshots")); err == nil && len(homeSnaps) > 0 {
		t.Fatalf("project promote must not write snapshots to home dir, found %d", len(homeSnaps))
	}

	if err := p.Rollback(&SkillEntry{Name: "build-flow", Path: activePath, Scope: "project"}); err != nil {
		t.Fatalf("project rollback failed: %v", err)
	}
	data, _ := os.ReadFile(activePath)
	if !strings.Contains(string(data), "v1-proj-old") {
		t.Fatalf("project rollback must restore old version, got:\n%s", string(data))
	}
}

// Scheduler startup must pre-create the home-level snapshot dir so a global
// rollback never depends on a prior global promote having run (issue fix
// suggestion #2: extend scheduler.go's startup dir list).
func TestIssue5114SchedulerEnsuresGlobalSnapshotDir(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()

	k := New(config.KnightConfig{Enabled: true, TrustLevel: "staged"}, home, proj, nil)
	if k == nil {
		t.Fatal("New returned nil")
	}
	defer k.Stop()
	if err := k.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".ggcode", "skills-snapshots")); err != nil {
		t.Fatalf("Knight startup must create home skills-snapshots dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".ggcode", "skills-snapshots")); err != nil {
		t.Fatalf("Knight startup must still create project skills-snapshots dir: %v", err)
	}
}
