//go:build goolm

package wailskit

import "testing"

// Issue #635: Save()'s read-merge unconditionally overwrote every bool field
// from the saving instance's full snapshot. A stale instance (one that never
// touched notifications/hotkey/on-top) rolling through SetWindowState+Save or
// the unconditional shutdown Save silently rolled back another instance's
// freshly saved preferences — exactly the multi-instance clobber #583 Bug 3
// claimed to prevent. Bools have no non-default sentinel, so per-field dirty
// flags gate the merge instead.

// StaleSnapshotDoesNotRollBackPreferences: instance B saves toggles; instance
// A (loaded before B's save, never touching those toggles) then saves after
// moving its window. B's preferences must survive.
func TestIssue635_StaleSnapshotPreservesOtherInstanceToggles(t *testing.T) {
	withTestHome(t)

	// Instance B: toggles notifications off, always-on-top on, hotkey off,
	// and a non-default font zoom, then saves.
	b := &DesktopConfig{}
	b.SetNotificationsEnabled(false)
	b.SetAlwaysOnTop(true)
	b.SetGlobalHotkey(false)
	b.SetFontZoom(1.4)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	// Instance A: stale snapshot (loaded before B's changes; its in-memory
	// toggles are all zero). It only moves the window and saves.
	a := &DesktopConfig{}
	a.SetWindowState(1000, 700, 10, 20, false)
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	got := LoadDesktopConfig()
	if got.IsNotificationsEnabled() {
		t.Fatal("stale snapshot re-enabled notifications another instance turned off")
	}
	if !got.IsAlwaysOnTop() {
		t.Fatal("stale snapshot rolled back always-on-top=true set by another instance")
	}
	if got.IsGlobalHotkeyEnabled() {
		t.Fatal("stale snapshot re-enabled the global hotkey another instance turned off")
	}
	if z := got.GetFontZoom(); z != 1.4 {
		t.Fatalf("font zoom rolled back by stale snapshot: got %v want 1.4", z)
	}
	// A's own dirty field (window bounds) must still be written.
	if got.WindowW != 1000 || got.WindowH != 700 || got.WindowX != 10 || got.WindowY != 20 {
		t.Fatalf("dirty window bounds not persisted: %+v", got)
	}
}

// ExplicitToggleWins: when this instance DID touch a toggle, its value must
// reach disk — the dirty flags must not over-suppress writes.
func TestIssue635_ExplicitToggleWins(t *testing.T) {
	withTestHome(t)

	// Disk starts with notifications configured OFF by another instance.
	b := &DesktopConfig{}
	b.SetNotificationsEnabled(false)
	b.SetAlwaysOnTop(true)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	// This instance explicitly re-enables notifications and unsets on-top.
	a := &DesktopConfig{}
	a.SetNotificationsEnabled(true)
	a.SetAlwaysOnTop(false)
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	got := LoadDesktopConfig()
	if !got.IsNotificationsEnabled() {
		t.Fatal("explicit re-enable was suppressed by the merge")
	}
	if got.IsAlwaysOnTop() {
		t.Fatal("explicit always-on-top=false was suppressed by the merge")
	}
}

// ShutdownSaveOfPristineSnapshotIsHarmless: the app.go shutdown path calls
// Save() unconditionally on a possibly-pristine snapshot; that must leave the
// disk file byte-equivalent in terms of every preference field.
func TestIssue635_PristineShutdownSaveNoop(t *testing.T) {
	withTestHome(t)

	b := &DesktopConfig{}
	b.SetGlobalHotkey(false)
	b.SetWindowState(1280, 860, 5, 5, true) // maximized
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	// Untouched instance shuts down and saves.
	if err := (&DesktopConfig{}).Save(); err != nil {
		t.Fatal(err)
	}

	got := LoadDesktopConfig()
	if got.IsGlobalHotkeyEnabled() {
		t.Fatal("pristine shutdown Save flipped the global hotkey back on")
	}
	if !got.WindowMax {
		t.Fatal("pristine shutdown Save cleared the maximized flag")
	}
}
