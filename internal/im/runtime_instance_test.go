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
