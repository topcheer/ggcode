package wailskit

import "testing"

// Issue #631 part 1: quitting while maximized used to persist the fullscreen
// bounds as the "normal" window size — the remembered normal bounds were
// destroyed and unmaximizing left a fullscreen-sized normal window. While
// maximized, only the flag must update.
func TestIssue631_MaximizedExitKeepsNormalBounds(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{}
	// Normal-mode usage first: capture real bounds.
	dc.SetWindowState(1440, 900, 120, 80, false)
	if dc.WindowW != 1440 || dc.WindowH != 900 || dc.WindowX != 120 || dc.WindowY != 80 {
		t.Fatalf("normal bounds not captured: %+v", dc)
	}

	// Now maximize and quit: the OS reports fullscreen bounds (e.g. 3008x1692
	// at 0,0). These must NOT overwrite the remembered normal bounds.
	dc.SetWindowState(3008, 1692, 0, 0, true)
	if !dc.WindowMax {
		t.Fatal("expected maximized flag")
	}
	if dc.WindowW != 1440 || dc.WindowH != 900 {
		t.Fatalf("maximized save clobbered normal size: %dx%d", dc.WindowW, dc.WindowH)
	}
	if dc.WindowX != 120 || dc.WindowY != 80 {
		t.Fatalf("maximized save clobbered normal position: %d,%d", dc.WindowX, dc.WindowY)
	}
}

// Issue #631 part 2: (0,0) is a legitimate window origin, not an "unset"
// sentinel. WindowPosSet disambiguates "no normal position ever captured"
// from "window deliberately parked at the top-left corner".
func TestIssue631_ZeroOriginIsExplicit(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{}
	if dc.WindowPosSet {
		t.Fatal("fresh config must not claim a captured position")
	}
	dc.SetWindowState(1000, 700, 0, 0, false)
	if !dc.WindowPosSet {
		t.Fatal("normal-mode save at (0,0) must set WindowPosSet")
	}
	if dc.WindowX != 0 || dc.WindowY != 0 {
		t.Fatalf("origin mutated: %d,%d", dc.WindowX, dc.WindowY)
	}

	// A maximized save must not manufacture a position-set flag either.
	dc2 := &DesktopConfig{}
	dc2.SetWindowState(3008, 1692, 0, 0, true)
	if dc2.WindowPosSet {
		t.Fatal("maximized save must not set WindowPosSet")
	}
}

// The position flag must survive the config save/load round-trip so restore
// on next launch honors a (0,0) origin.
func TestIssue631_PosSetRoundTrip(t *testing.T) {
	withTestHome(t)
	dc := &DesktopConfig{}
	dc.SetWindowState(1000, 700, 0, 0, false)
	if err := dc.Save(); err != nil {
		t.Fatal(err)
	}
	loaded := LoadDesktopConfig()
	if !loaded.WindowPosSet {
		t.Fatal("WindowPosSet lost in round-trip — (0,0) origin treated as unset on restore")
	}
	if loaded.WindowX != 0 || loaded.WindowY != 0 {
		t.Fatalf("origin changed in round-trip: %d,%d", loaded.WindowX, loaded.WindowY)
	}
}
