//go:build darwin && goolm

package main

// Issue #615 regression tests (darwin build): SetGlobalHotkeyEnabled must
// surface registration failure and roll back the persisted preference, and
// the 200ms poller must be a single reusable goroutine instead of one leak
// per enable.

import (
	"errors"
	"testing"
	"time"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

// errIssue615SimulatedHotkey stands in for a failed RegisterEventHotKey
// (combo exclusively owned by another app).
var errIssue615SimulatedHotkey = errors.New("simulated RegisterEventHotKey failure (combo in use)")

// newIssue615App returns an App whose hotkey registration is replaced by a
// hook — the real RegisterEventHotKey fails on machines where another
// instance already owns Option+Cmd+G (OSStatus -9866), so tests must not
// depend on the physical registration outcome.
func newIssue615App(t *testing.T, hotkeyEnabled bool, regErr error) *App {
	t.Helper()
	a := NewApp()
	a.dc = wailskit.LoadDesktopConfig()
	a.dc.SetGlobalHotkey(hotkeyEnabled)
	a.hotkeyRegisterHook = func() error { return regErr }
	return a
}

// D1: registration failure (simulated combo-in-use) must return an error
// and roll the persisted preference back to its previous value.
func TestIssue615_RegisterFailureRollsBackPersistedState(t *testing.T) {
	a := newIssue615App(t, false, errIssue615SimulatedHotkey)

	err := a.SetGlobalHotkeyEnabled(true)
	if !errors.Is(err, errIssue615SimulatedHotkey) {
		t.Fatalf("SetGlobalHotkeyEnabled(true) = %v, want simulated registration error", err)
	}
	if a.dc.IsGlobalHotkeyEnabled() {
		t.Error("persisted hotkey preference shows enabled despite failed registration (#615 D1)")
	}

	// Disabling while already failed must still succeed and stay disabled.
	if err := a.SetGlobalHotkeyEnabled(false); err != nil {
		t.Fatalf("SetGlobalHotkeyEnabled(false): %v", err)
	}
	if a.dc.IsGlobalHotkeyEnabled() {
		t.Error("disable did not persist")
	}
}

// D1 (success path): enabling with a free combo persists enabled=true.
func TestIssue615_EnablePersistsWhenRegistrationSucceeds(t *testing.T) {
	a := newIssue615App(t, false, nil)
	if err := a.SetGlobalHotkeyEnabled(true); err != nil {
		t.Fatalf("SetGlobalHotkeyEnabled(true): %v", err)
	}
	if !a.dc.IsGlobalHotkeyEnabled() {
		t.Error("successful registration did not persist enabled=true")
	}
}

// D2: repeated enable cycles must keep exactly one poller goroutine.
func TestIssue615_SinglePollerAcrossToggleCycles(t *testing.T) {
	a := newIssue615App(t, false, nil)

	for i := 0; i < 3; i++ {
		if err := a.SetGlobalHotkeyEnabled(true); err != nil {
			t.Fatalf("cycle %d enable: %v", i, err)
		}
		if !hotkeyPollerRunning() {
			t.Fatalf("cycle %d: poller not running after enable", i)
		}
		if err := a.SetGlobalHotkeyEnabled(false); err != nil {
			t.Fatalf("cycle %d disable: %v", i, err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for hotkeyPollerRunning() {
			if time.Now().After(deadline) {
				t.Fatalf("cycle %d: poller still running after disable (#615 D2 leak)", i)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Double enable must not stack a second poller.
	if err := a.SetGlobalHotkeyEnabled(true); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if err := a.SetGlobalHotkeyEnabled(true); err != nil {
		t.Fatalf("second enable: %v", err)
	}
	if !hotkeyPollerRunning() {
		t.Fatal("poller not running after double enable")
	}
	_ = a.SetGlobalHotkeyEnabled(false)
}

// Direct unit: stopHotkeyPoller without a running poller must not panic
// (disable path when registration previously failed).
func TestIssue615_StopPollerIdempotent(t *testing.T) {
	a := NewApp()
	a.stopHotkeyPoller() // no-op, must not panic
	a.stopHotkeyPoller()
}
