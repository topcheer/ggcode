//go:build goolm

package wailskit

import "testing"

func TestIssue710_StaleShutdownSavePreservesOtherInstanceWorkDir(t *testing.T) {
	withTestHome(t)

	a := &DesktopConfig{}
	a.SetWorkDir("/proj1")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	b := &DesktopConfig{WorkDir: "/proj1"}
	b.SetWorkDir("/proj2")
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	a.SetWindowState(1100, 750, 5, 5, false)
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	if got := LoadDesktopConfig().WorkDir; got != "/proj2" {
		t.Fatalf("stale shutdown Save rolled back other instance's workspace switch: got %q want /proj2", got)
	}

	if got := LoadDesktopConfig(); got.WindowW != 1100 || got.WindowH != 750 {
		t.Fatalf("dirty window bounds not persisted: %+v", got)
	}
}

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

func TestIssue710_RawSnapshotWorkDirIgnored(t *testing.T) {
	withTestHome(t)

	a := &DesktopConfig{}
	a.SetWorkDir("/proj1")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	stale := &DesktopConfig{WorkDir: "/stale-value"}
	stale.SetWindowState(800, 600, 0, 0, false)
	if err := stale.Save(); err != nil {
		t.Fatal(err)
	}
	if got := LoadDesktopConfig().WorkDir; got != "/proj1" {
		t.Fatalf("non-dirty WorkDir overwrote on-disk value: got %q want /proj1", got)
	}
}
