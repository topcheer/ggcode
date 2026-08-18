package im

// Issue #693 (follow-up to #689): UnmuteBinding's ownership check had no
// dead-owner takeover path, and UnmuteAll bypassed the check entirely.
//
// Part 1: a foreign-owned muted binding must be reclaimable when the owning
// session's instance is dead (crash/kill -9 never runs exit cleanup, so
// LastSessionID stays set and the binding would otherwise be muted forever).
//
// Part 2: UnmuteAll must apply the same ownership rule as UnmuteBinding —
// live foreign owners are skipped; dead owners' bindings are taken over.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeInstanceDetect builds an InstanceDetect whose directory is pre-seeded
// with instance files for the given PIDs, with a controllable liveness probe.
// The Manager under test gets its own registered entry (its real PID+UUID).
// Other instances' files are written directly (no Register) so its cleanup
// logic cannot remove them.
func fakeInstanceDetect(t *testing.T, mgr *Manager, otherPIDs []int, alive func(pid int) bool) {
	t.Helper()
	dir := t.TempDir()
	d := NewInstanceDetect(dir)
	d.mu.Lock()
	d.checkAlive = alive
	d.mu.Unlock()
	if _, err := d.Register(); err != nil {
		t.Fatalf("register instance: %v", err)
	}
	// Seed other instances' PID files so ListInstances sees them.
	instancesPath := filepath.Join(dir, ".ggcode", instancesDir)
	for i, pid := range otherPIDs {
		data, err := json.Marshal(InstanceInfo{PID: pid, UUID: fakeUUID(pid), StartedAt: time.Now().Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatalf("marshal instance pid=%d: %v", pid, err)
		}
		name := fmt.Sprintf("%d-%s.json", pid, fakeUUID(pid)[:8])
		if err := os.WriteFile(filepath.Join(instancesPath, name), data, 0o644); err != nil {
			t.Fatalf("write instance file pid=%d: %v", pid, err)
		}
	}
	mgr.mu.Lock()
	mgr.instanceDetect = d
	mgr.mu.Unlock()
}

func fakeUUID(pid int) string {
	// Deterministic distinct UUID per pid; only uniqueness matters here.
	const hex = "0123456789abcdef"
	u := "00000000-0000-4000-8000-"
	for i := 0; i < 12; i++ {
		u += string(hex[pid%16])
	}
	return u
}

func (d *InstanceDetect) dirOf694() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dir
}

// Part 1a: dead owner (no other live instance) — UnmuteBinding must take over
// the orphan binding instead of a hard denial.
func TestIssue693_UnmuteTakesOverDeadOwnersBinding(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "dead-session", // orphan: owner crashed, never cleaned up
		Muted:         true,
	}
	mgr.mu.Unlock()

	// No other live instance: only self is registered → owner must be dead.
	fakeInstanceDetect(t, mgr, nil, func(pid int) bool { return true })

	if err := mgr.UnmuteBinding("tg"); err != nil {
		t.Fatalf("#693: dead-owner takeover denied: %v", err)
	}
	if mgr.IsBindingMuted("tg") {
		t.Fatal("#693: binding should be unmuted after takeover")
	}
	b, ok := mgr.getBindingForTest("tg")
	if !ok || b.LastSessionID != "test-session" {
		t.Fatalf("#693: takeover did not re-claim binding (last=%q)", b.LastSessionID)
	}
}

// Part 1b: live owner (another live instance exists) — rejection stands,
// preserving #689's hijack guarantee. #693 only allows DEAD owners.
func TestIssue693_UnmuteStillRejectsLiveForeignOwner(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "live-other-session",
		Muted:         true,
	}
	mgr.mu.Unlock()

	// Another live instance besides self.
	fakeInstanceDetect(t, mgr, []int{424242}, func(pid int) bool { return true })

	if err := mgr.UnmuteBinding("tg"); err == nil {
		t.Fatal("#693: unmute of a LIVE foreign owner's binding succeeded — #689 hijack regression")
	}
	if !mgr.IsBindingMuted("tg") {
		t.Fatal("#693: live foreign owner's binding must stay muted")
	}
}

// Part 2a: UnmuteAll skips bindings owned by a live foreign session instead of
// silently grabbing them for the ~3s window before the binding watcher re-mutes.
func TestIssue693_UnmuteAllSkipsLiveForeignOwner(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "live-other-session",
		Muted:         true,
	}
	mgr.currentBindings["own"] = &ChannelBinding{
		Adapter:       "own",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "test-session",
		Muted:         true,
	}
	mgr.mu.Unlock()

	fakeInstanceDetect(t, mgr, []int{424243}, func(pid int) bool { return true })

	count, err := mgr.UnmuteAll()
	if err != nil {
		t.Fatalf("unmute all: %v", err)
	}
	if count != 1 {
		t.Fatalf("#693: UnmuteAll unmuted %d adapters, want 1 (foreign-owned must be skipped)", count)
	}
	if !mgr.IsBindingMuted("tg") {
		t.Fatal("#693: UnmuteAll unmuted a binding owned by a live foreign session")
	}
	if mgr.IsBindingMuted("own") {
		t.Fatal("own muted binding should have been unmuted")
	}
}

// Part 2b: UnmuteAll takes over a dead owner's orphan binding, mirroring
// UnmuteBinding's takeover path.
func TestIssue693_UnmuteAllTakesOverDeadOwnersBinding(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "dead-session",
		Muted:         true,
	}
	mgr.mu.Unlock()

	// Only a stale PID file exists (other instance crashed) → not alive.
	fakeInstanceDetect(t, mgr, []int{999999}, func(pid int) bool { return pid != 999999 })

	count, err := mgr.UnmuteAll()
	if err != nil {
		t.Fatalf("unmute all: %v", err)
	}
	if count != 1 {
		t.Fatalf("#693: UnmuteAll unmuted %d adapters, want 1 (dead owner should be taken over)", count)
	}
	b, ok := mgr.getBindingForTest("tg")
	if !ok || b.LastSessionID != "test-session" {
		t.Fatalf("#693: UnmuteAll takeover did not re-claim binding (last=%q)", b.LastSessionID)
	}
}

// Guard: unregistered instanceDetect (nil) keeps the conservative #689
// rejection — dead-owner takeover requires a liveness registry.
func TestIssue693_NoDetectorKeepsConservativeRejection(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "other-session",
		Muted:         true,
	}
	mgr.mu.Unlock()

	if err := mgr.UnmuteBinding("tg"); err == nil {
		t.Fatal("#693: without an instance registry the foreign binding must stay rejected")
	}
}
