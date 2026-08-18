//go:build goolm

package wailskit

import "testing"

// Issue #710: WorkDir was left out of the #635 dirty-flag protection. It
// merged through the non-empty guard, which is ineffective for this field —
// WorkDir is almost always non-empty in any loaded instance. So a stale
// instance's close-time Save rolled back the workspace another instance had
// just switched to. Fix: dirtyWorkDir flag (set by SetWorkDir, cleared by a
// successful Save), gating the merge exactly like the five #635 groups.

// StaleShutdownSaveMustNotRollBackWorkspaceSwitch: the issue's exact trigger.
// A and B both start on /proj1; B switches to /proj2 and saves; A (which
// previously persisted /proj1 itself, so its flag was consumed) then performs
// the unconditional shutdown Save. B's switch must survive.
func TestIssue710_StaleShutdownSavePreservesOtherInstanceWorkDir(t *testing.T) {
	withTestHome(t)

	// Instance A: starts up on /proj1 (initWorkspace → SetWorkDir + Save).
	a := &DesktopConfig{}
	a.SetWorkDir("/proj1")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	// Instance B: loaded the same config (/proj1), then the user switches
	// the workspace to /proj2 (SetWorkDir + Save). Disk now says /proj2.
	b := &DesktopConfig{WorkDir: "/proj1"}
	b.SetWorkDir("/proj2")
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	// Instance A later closes: unconditional shutdown Save after a window
	// move. Its in-memory WorkDir is the stale /proj1. Pre-#710 the
	// non-empty guard let that stale value win and disk rolled back to
	// /proj1; with the dirty flag it must not.
	a.SetWindowState(1100, 750, 5, 5, false)
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	if got := LoadDesktopConfig().WorkDir; got != "/proj2" {
		t.Fatalf("stale shutdown Save rolled back other instance's workspace switch: got %q want /proj2", got)
	}
	// A's own dirty field (window bounds) must still be written.
	if got := LoadDesktopConfig(); got.WindowW != 1100 || got.WindowH != 750 {
		t.Fatalf("dirty window bounds not persisted: %+v", got)
	}
}

// ExplicitSwitchWins: a genuinely fresh SetWorkDir since the last Save must
// reach disk — the dirty flag must not over-suppress writes.
func TestIssue710_ExplicitSwitchWins(t *testing.T) {
	withTestHome(t)

	a := &DesktopConfig{}
	a.SetWorkDir("/proj1")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	a.SetWorkDir("/proj2")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if got := LoadDesktopConfig().WorkDir; got != "/proj2" {
		t.Fatalf("explicit workspace switch not persisted: got %q want /proj2", got)
	}
}

// RawSnapshotWorkDirIgnored: a struct literal (or stale loaded snapshot) that
// never ran SetWorkDir carries no authority over the merged WorkDir — mirrors
// the #647 raw-literal bounds semantics.
func TestIssue710_RawSnapshotWorkDirIgnored(t *testing.T) {
	withTestHome(t)

	a := &DesktopConfig{}
	a.SetWorkDir("/proj1")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	// Stale snapshot with a (different) non-empty WorkDir and no setter run.
	stale := &DesktopConfig{WorkDir: "/stale-value"}
	stale.SetWindowState(800, 600, 0, 0, false) // unrelated dirty field
	if err := stale.Save(); err != nil {
		t.Fatal(err)
	}
	if got := LoadDesktopConfig().WorkDir; got != "/proj1" {
		t.Fatalf("non-dirty WorkDir overwrote on-disk value: got %q want /proj1", got)
	}
}
