package im

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

func TestIssue693_UnmuteTakesOverDeadOwnersBinding(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "dead-session",
		Muted:         true,
	}
	mgr.mu.Unlock()

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

	fakeInstanceDetect(t, mgr, []int{424242}, func(pid int) bool { return true })

	if err := mgr.UnmuteBinding("tg"); err == nil {
		t.Fatal("#693: unmute of a LIVE foreign owner's binding succeeded — #689 hijack regression")
	}
	if !mgr.IsBindingMuted("tg") {
		t.Fatal("#693: live foreign owner's binding must stay muted")
	}
}

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
