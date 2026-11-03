package im

import (
	"testing"
	"time"
)

func TestIssue689_RebindPurgesDisabledTombstone(t *testing.T) {
	mgr, _ := testDummyManager()

	if err := mgr.BindAdapterToWorkspace("tg", "/tmp/test-workspace"); err != nil {
		t.Fatalf("bind to session workspace: %v", err)
	}
	if err := mgr.DisableBinding("tg"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !mgr.IsBindingDisabled("tg") {
		t.Fatal("expected binding disabled")
	}

	if err := mgr.BindAdapterToWorkspace("tg", "/tmp/test-workspace"); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	if mgr.IsBindingDisabled("tg") {
		t.Fatal("#689: stale disabledBindings tombstone survived rebind; EnableBinding would resurrect the pre-rebind copy (ghost)")
	}

	b, ok := mgr.getBindingForTest("tg")
	if !ok {
		t.Fatal("#689: fresh binding not active after rebind (dead adapter until restart)")
	}
	if b.Workspace != "/tmp/test-workspace" {
		t.Fatalf("active binding workspace = %q, want /tmp/test-workspace", b.Workspace)
	}
}

func (m *Manager) getBindingForTest(adapter string) (*ChannelBinding, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.currentBindings[adapter]
	return b, ok
}

func TestIssue689_UnmuteOwnershipCheck(t *testing.T) {
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
		t.Fatal("#689: unmute of another session's binding succeeded — channel hijack via UpdateSessionID rewrite")
	}
}

func TestIssue689_UnmuteOwnBindingStillWorks(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "test-session",
		Muted:         true,
	}
	mgr.mu.Unlock()

	if err := mgr.UnmuteBinding("tg"); err != nil {
		t.Fatalf("unmute own binding: %v", err)
	}
	if mgr.IsBindingMuted("tg") {
		t.Fatal("binding should be unmuted")
	}
}

func TestIssue689_ReloadWithoutSessionKeepsMemoryState(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())

	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:   "tg",
		Workspace: "/tmp/pre-session",
		Muted:     true,
		BoundAt:   time.Now(),
	}
	if err := mgr.reloadBindingLocked(); err != nil {
		mgr.mu.Unlock()
		t.Fatalf("reload: %v", err)
	}
	_, stillBound := mgr.currentBindings["tg"]
	mgr.mu.Unlock()

	if !stillBound {
		t.Fatal("#689: pre-session reload wiped in-memory bindings")
	}
	if !mgr.IsBindingMuted("tg") {
		t.Fatal("#689: pre-session reload discarded muted state")
	}
}
