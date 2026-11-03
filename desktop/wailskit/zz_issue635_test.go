//go:build goolm

package wailskit

import "testing"

func TestIssue635_StaleSnapshotPreservesOtherInstanceToggles(t *testing.T) {
	withTestHome(t)

	b := &DesktopConfig{}
	b.SetNotificationsEnabled(false)
	b.SetAlwaysOnTop(true)
	b.SetGlobalHotkey(false)
	b.SetFontZoom(1.4)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

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

	if got.WindowW != 1000 || got.WindowH != 700 || got.WindowX != 10 || got.WindowY != 20 {
		t.Fatalf("dirty window bounds not persisted: %+v", got)
	}
}

func TestIssue635_ExplicitToggleWins(t *testing.T) {
	withTestHome(t)

	b := &DesktopConfig{}
	b.SetNotificationsEnabled(false)
	b.SetAlwaysOnTop(true)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

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

func TestIssue635_PristineShutdownSaveNoop(t *testing.T) {
	withTestHome(t)

	b := &DesktopConfig{}
	b.SetGlobalHotkey(false)
	b.SetWindowState(1280, 860, 5, 5, true)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

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
