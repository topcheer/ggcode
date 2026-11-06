package knight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #5114: snapshot storage must follow the skill's scope, mirroring
// activeDir/stagingDir. createSnapshot/listSnapshots hardcoded projectDir,
// so a global-scope skill's snapshots landed in the project tree: rollback
// broke from any other project dir and project view got polluted.
func TestIssue5114GlobalSnapshotUsesHomeDir(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	p := NewPromoter(home, proj)

	active := filepath.Join(home, ".ggcode", "skills", "g", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(active), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, active, "v1")
	if err := p.createSnapshot("g", active, "global"); err != nil {
		t.Fatal(err)
	}

	projSnap := filepath.Join(proj, ".ggcode", "skills-snapshots")
	if _, err := os.Stat(projSnap); err == nil {
		des, _ := os.ReadDir(projSnap)
		if len(des) > 0 {
			t.Errorf("#5114 global snapshot leaked into project tree: %v", des)
		}
	}

	homeSnaps, err := p.listSnapshots("g", "global")
	if err != nil {
		t.Fatal(err)
	}
	if len(homeSnaps) != 1 {
		t.Fatalf("global snapshot not visible via listSnapshots(global), got %d: %v", len(homeSnaps), homeSnaps)
	}
	if !strings.HasPrefix(homeSnaps[0], filepath.Join(home, ".ggcode", "skills-snapshots")) {
		t.Errorf("global snapshot must live under homeDir snapshots dir, got %s", homeSnaps[0])
	}

	// Project scope still routes to the project tree.
	psnaps, err := p.listSnapshots("g", "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(psnaps) != 0 {
		t.Errorf("project-scope listing must not see global snapshots, got %v", psnaps)
	}

	// Unknown scope must be rejected, not silently misrouted.
	if _, err := p.snapshotDir("weird"); err == nil {
		t.Error("snapshotDir must reject unknown scope")
	}
}
