//go:build darwin && goolm

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/topcheer/ggcode/desktop/wailskit"
)

var errIssue615SimulatedHotkey = errors.New("simulated RegisterEventHotKey failure (combo in use)")

func newIssue615App(t *testing.T, hotkeyEnabled bool, regErr error) *App {
	t.Helper()
	a := NewApp()
	a.dc = wailskit.LoadDesktopConfig()
	a.dc.SetGlobalHotkey(hotkeyEnabled)
	a.hotkeyRegisterHook = func() error { return regErr }
	return a
}

func TestIssue615_RegisterFailureRollsBackPersistedState(t *testing.T) {
	a := newIssue615App(t, false, errIssue615SimulatedHotkey)

	err := a.SetGlobalHotkeyEnabled(true)
	if !errors.Is(err, errIssue615SimulatedHotkey) {
		t.Fatalf("SetGlobalHotkeyEnabled(true) = %v, want simulated registration error", err)
	}
	if a.dc.IsGlobalHotkeyEnabled() {
		t.Error("persisted hotkey preference shows enabled despite failed registration (#615 D1)")
	}

	if err := a.SetGlobalHotkeyEnabled(false); err != nil {
		t.Fatalf("SetGlobalHotkeyEnabled(false): %v", err)
	}
	if a.dc.IsGlobalHotkeyEnabled() {
		t.Error("disable did not persist")
	}
}

func TestIssue615_EnablePersistsWhenRegistrationSucceeds(t *testing.T) {
	a := newIssue615App(t, false, nil)
	if err := a.SetGlobalHotkeyEnabled(true); err != nil {
		t.Fatalf("SetGlobalHotkeyEnabled(true): %v", err)
	}
	if !a.dc.IsGlobalHotkeyEnabled() {
		t.Error("successful registration did not persist enabled=true")
	}
}

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

func TestIssue615_StopPollerIdempotent(t *testing.T) {
	a := NewApp()
	a.stopHotkeyPoller()
	a.stopHotkeyPoller()
}
