package im

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerRegisterInstance_Single(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager()
	mgr.SetBindingStore(&memBindingStore{
		bindings: []ChannelBinding{
			{Workspace: dir, Platform: PlatformQQ, Adapter: "qq-bot-1", ChannelID: "ch-1"},
		},
	})
	// BindSession loads bindings from the store (same as real root.go/daemon.go flow)
	mgr.BindSession(SessionBinding{Workspace: dir})

	detect, others, err := mgr.RegisterInstance(dir)
	if err != nil {
		t.Fatalf("RegisterInstance: %v", err)
	}
	if detect == nil {
		t.Fatal("expected non-nil detect")
	}
	if len(others) != 0 {
		t.Fatalf("expected 0 others, got %d", len(others))
	}
	if !mgr.IsPrimary() {
		t.Fatal("single instance should be primary")
	}
	if !detect.IsRegistered() {
		t.Fatal("should be registered")
	}

	// Verify PID file was written
	entries, _ := os.ReadDir(filepath.Join(dir, ".ggcode", "instances"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 PID file, got %d", len(entries))
	}

	// HasActiveChannels should be true (1 non-muted binding with ChannelID)
	detect.mu.Lock()
	info := detect.info
	detect.mu.Unlock()
	if !info.HasActiveChannels {
		t.Fatal("expected HasActiveChannels=true")
	}

	// Cleanup
	mgr.UnregisterInstance()
	if mgr.InstanceDetect() != nil {
		t.Fatal("expected nil detect after unregister")
	}
}

func TestManagerRegisterInstance_Secondary(t *testing.T) {
	dir := t.TempDir()
	instancesDir := filepath.Join(dir, ".ggcode", "instances")
	os.MkdirAll(instancesDir, 0o755)

	// Simulate an older instance
	sleepCmd := exec.Command("sleep", "30")
	if err := sleepCmd.Start(); err != nil {
		t.Skip("cannot fork sleep process")
	}
	defer sleepCmd.Process.Kill()
	fakePID := sleepCmd.Process.Pid

	olderInfo := InstanceInfo{
		PID:       fakePID,
		UUID:      "older-uuid-abcd",
		StartedAt: time.Now().Add(-5 * time.Minute),
	}
	olderData, _ := json.Marshal(olderInfo)
	os.WriteFile(filepath.Join(instancesDir, fmt.Sprintf("%d-older-uu.json", fakePID)), olderData, 0o644)

	mgr := NewManager()
	mgr.SetBindingStore(&memBindingStore{
		bindings: []ChannelBinding{
			{Workspace: dir, Platform: PlatformQQ, Adapter: "qq-bot-1", ChannelID: "ch-1"},
			{Workspace: dir, Platform: PlatformTelegram, Adapter: "tg-bot-1", ChannelID: "ch-2"},
		},
	})
	// BindSession loads bindings from the store
	mgr.BindSession(SessionBinding{Workspace: dir})

	detect, others, err := mgr.RegisterInstance(dir)
	if err != nil {
		t.Fatalf("RegisterInstance: %v", err)
	}
	if len(others) != 1 {
		t.Fatalf("expected 1 other, got %d", len(others))
	}
	if others[0].PID != fakePID {
		t.Fatalf("expected other PID=%d, got %d", fakePID, others[0].PID)
	}

	// Should NOT be primary
	if mgr.IsPrimary() {
		t.Fatal("second instance should NOT be primary")
	}

	// All bindings should be muted
	for _, b := range mgr.CurrentBindings() {
		if !b.Muted {
			t.Fatalf("expected binding %s to be muted", b.Adapter)
		}
	}
	muted := mgr.MutedBindings()
	if len(muted) != 2 {
		t.Fatalf("expected 2 muted, got %d", len(muted))
	}

	// HasActiveChannels should be false (all muted)
	detect.mu.Lock()
	info := detect.info
	detect.mu.Unlock()
	if info.HasActiveChannels {
		t.Fatal("expected HasActiveChannels=false (all muted)")
	}

	mgr.UnregisterInstance()
}

func TestManagerRegisterInstance_Idempotent(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager()

	detect1, _, err := mgr.RegisterInstance(dir)
	if err != nil {
		t.Fatalf("first RegisterInstance: %v", err)
	}
	if !detect1.IsRegistered() {
		t.Fatal("expected first detect to be registered")
	}

	// After first registration, the detector is stored on the Manager
	if mgr.InstanceDetect() != detect1 {
		t.Fatal("expected detect stored on manager")
	}

	// Calling RegisterInstance again would create a NEW detector.
	// Callers should check mgr.InstanceDetect() first (as detectAndAutoMute does).
	mgr.UnregisterInstance()
	if mgr.InstanceDetect() != nil {
		t.Fatal("expected nil detect after unregister")
	}

	// After unregister, PID file should be cleaned up
	entries, _ := os.ReadDir(filepath.Join(dir, ".ggcode", "instances"))
	if len(entries) != 0 {
		t.Fatalf("expected 0 PID files after unregister, got %d", len(entries))
	}
}

func TestManagerSyncActiveChannels(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager()
	mgr.SetBindingStore(&memBindingStore{
		bindings: []ChannelBinding{
			{Workspace: dir, Platform: PlatformQQ, Adapter: "qq-bot-1", ChannelID: "ch-1"},
		},
	})
	// BindSession loads bindings from the store
	mgr.BindSession(SessionBinding{Workspace: dir})

	detect, _, _ := mgr.RegisterInstance(dir)

	// Initially: HasActiveChannels=true (binding has ChannelID, not muted)
	detect.mu.Lock()
	info := detect.info
	detect.mu.Unlock()
	if !info.HasActiveChannels {
		t.Fatal("expected HasActiveChannels=true initially")
	}

	// Mute all → HasActiveChannels should become false
	mgr.MuteAll()
	detect.mu.Lock()
	info = detect.info
	detect.mu.Unlock()
	if info.HasActiveChannels {
		t.Fatal("expected HasActiveChannels=false after MuteAll")
	}

	// Unmute all → HasActiveChannels should become true again
	mgr.SetOnRestart(func(adapterName string) error { return nil })
	mgr.UnmuteAll()
	detect.mu.Lock()
	info = detect.info
	detect.mu.Unlock()
	if !info.HasActiveChannels {
		t.Fatal("expected HasActiveChannels=true after UnmuteAll")
	}

	mgr.UnregisterInstance()
}

func TestManagerIsPrimary_NoDetector(t *testing.T) {
	mgr := NewManager()
	// No detector → assume primary
	if !mgr.IsPrimary() {
		t.Fatal("expected IsPrimary=true when no detector set")
	}
}

// TestReloadBindingPreservesMutedState is a regression test for the bug where
// calling BindSession after RegisterInstance+MuteAll would wipe the auto-mute
// state. This simulates the real flow: InitRuntime → RegisterInstance (auto-mute)
// → SetIMManager → bindIMSession → BindSession → reloadBindingLocked.
func TestReloadBindingPreservesMutedState(t *testing.T) {
	dir := t.TempDir()
	instancesDir := filepath.Join(dir, ".ggcode", "instances")
	os.MkdirAll(instancesDir, 0o755)

	// Simulate a primary instance already running
	sleepCmd := exec.Command("sleep", "30")
	if err := sleepCmd.Start(); err != nil {
		t.Skip("cannot fork sleep process")
	}
	defer sleepCmd.Process.Kill()
	fakePID := sleepCmd.Process.Pid

	olderInfo := InstanceInfo{
		PID:               fakePID,
		UUID:              "primary-uuid-abcd",
		StartedAt:         time.Now().Add(-5 * time.Minute),
		HasActiveChannels: true,
	}
	olderData, _ := json.Marshal(olderInfo)
	os.WriteFile(filepath.Join(instancesDir, fmt.Sprintf("%d-primary-u.json", fakePID)), olderData, 0o644)

	store := &memBindingStore{
		bindings: []ChannelBinding{
			{Workspace: dir, Platform: PlatformQQ, Adapter: "qq-bot-1", ChannelID: "ch-1"},
			{Workspace: dir, Platform: PlatformTelegram, Adapter: "tg-bot-1", ChannelID: "ch-2"},
		},
	}
	mgr := NewManager()
	mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{Workspace: dir})

	// RegisterInstance should auto-mute all bindings (non-primary instance)
	_, others, err := mgr.RegisterInstance(dir)
	if err != nil {
		t.Fatalf("RegisterInstance: %v", err)
	}
	if len(others) != 1 {
		t.Fatalf("expected 1 other, got %d", len(others))
	}

	// Verify all bindings are muted after RegisterInstance
	for _, b := range mgr.CurrentBindings() {
		if !b.Muted {
			t.Fatalf("expected binding %s to be muted after RegisterInstance", b.Adapter)
		}
	}

	// Simulate bindIMSession: call BindSession again (same workspace).
	// Before the fix, reloadBindingLocked would reset Muted=false for all bindings.
	mgr.BindSession(SessionBinding{Workspace: dir})

	// After the fix: muted state should be preserved
	for _, b := range mgr.CurrentBindings() {
		if !b.Muted {
			t.Fatalf("REGRESSION: binding %s lost muted state after BindSession reload", b.Adapter)
		}
	}

	mgr.UnregisterInstance()
}

// TestReloadBindingSkipsDisabled tests that reloadBindingLocked does not
// re-add adapters that were explicitly disabled via ApplyAdapterConfig.
func TestReloadBindingSkipsDisabled(t *testing.T) {
	dir := t.TempDir()
	store := &memBindingStore{
		bindings: []ChannelBinding{
			{Workspace: dir, Platform: PlatformQQ, Adapter: "qq-bot-1", ChannelID: "ch-1"},
			{Workspace: dir, Platform: PlatformTelegram, Adapter: "tg-bot-1", ChannelID: "ch-2"},
		},
	}
	mgr := NewManager()
	mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{Workspace: dir})

	// Verify both adapters loaded
	if len(mgr.CurrentBindings()) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(mgr.CurrentBindings()))
	}

	// Disable one adapter
	mgr.ApplyAdapterConfig(map[string]bool{"qq-bot-1": false})
	if len(mgr.CurrentBindings()) != 1 {
		t.Fatalf("expected 1 active binding after disable, got %d", len(mgr.CurrentBindings()))
	}
	if _, ok := mgr.CurrentBindings()[0], mgr.IsBindingDisabled("qq-bot-1"); !ok {
		// just check IsBindingDisabled
	}
	if !mgr.IsBindingDisabled("qq-bot-1") {
		t.Fatal("expected qq-bot-1 to be disabled")
	}

	// Reload via BindSession — disabled adapter should NOT come back
	mgr.BindSession(SessionBinding{Workspace: dir})
	if len(mgr.CurrentBindings()) != 1 {
		t.Fatalf("expected 1 active binding after reload (disabled should stay disabled), got %d", len(mgr.CurrentBindings()))
	}
	for _, b := range mgr.CurrentBindings() {
		if b.Adapter == "qq-bot-1" {
			t.Fatal("REGRESSION: disabled adapter qq-bot-1 came back after reload")
		}
	}
}

// memBindingStore is a minimal in-memory BindingStore for testing.
type memBindingStore struct {
	bindings []ChannelBinding
}

func (s *memBindingStore) Save(b ChannelBinding) error {
	for i, existing := range s.bindings {
		if existing.Workspace == b.Workspace && existing.Adapter == b.Adapter {
			s.bindings[i] = b
			return nil
		}
	}
	s.bindings = append(s.bindings, b)
	return nil
}
func (s *memBindingStore) Delete(workspace, adapter string) error {
	for i, b := range s.bindings {
		if b.Workspace == workspace && b.Adapter == adapter {
			s.bindings = append(s.bindings[:i], s.bindings[i+1:]...)
			return nil
		}
	}
	return nil
}
func (s *memBindingStore) List() ([]ChannelBinding, error) {
	return append([]ChannelBinding{}, s.bindings...), nil
}
func (s *memBindingStore) ListByWorkspace(ws string) ([]ChannelBinding, error) {
	var result []ChannelBinding
	for _, b := range s.bindings {
		if b.Workspace == ws {
			result = append(result, b)
		}
	}
	return result, nil
}
func (s *memBindingStore) ListByAdapter(adapter string) ([]ChannelBinding, error) {
	var result []ChannelBinding
	for _, b := range s.bindings {
		if b.Adapter == adapter {
			result = append(result, b)
		}
	}
	return result, nil
}
func (s *memBindingStore) BindExclusive(binding ChannelBinding) error {
	// Remove all existing bindings for this adapter
	filtered := s.bindings[:0]
	for _, b := range s.bindings {
		if b.Adapter != binding.Adapter {
			filtered = append(filtered, b)
		}
	}
	s.bindings = filtered
	// Append new binding
	binding.Workspace = normalizeWorkspace(binding.Workspace)
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	s.bindings = append(s.bindings, binding)
	return nil
}
