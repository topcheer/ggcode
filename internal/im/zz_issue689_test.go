package im

// Issue #689: binding lifecycle defects in internal/im.
//
// Defect 1 (HIGH): BindAdapterToWorkspace never purged the adapter's
// disabledBindings tombstone — after disable→rebind the fresh binding was
// skipped by reloadBindingLocked (dead adapter until restart) and EnableBinding
// resurrected the stale pre-rebind copy (ghost, #434).
//
// Defect 2 (MED-HIGH): UnmuteBinding had no ownership check — instance B
// could unmute session A's foreign-muted binding and rewrite LastSessionID,
// silently stealing the channel.
//
// Defect 3 (MED): reloadBindingLocked cleared currentBindings BEFORE the
// bindingStore==nil / session==nil early return, wiping all in-memory
// bindings and the collected prevMuted state on the pre-session path.

import (
	"testing"
	"time"
)

// Defect 1: rebind must clear the old disabled tombstone.
func TestIssue689_RebindPurgesDisabledTombstone(t *testing.T) {
	mgr, _ := testDummyManager()

	// Bind adapter to the session workspace, then disable it (tombstone
	// created). reloadBindingLocked only loads bindings for the session
	// workspace, so the rebind below must also target it.
	if err := mgr.BindAdapterToWorkspace("tg", "/tmp/test-workspace"); err != nil {
		t.Fatalf("bind to session workspace: %v", err)
	}
	if err := mgr.DisableBinding("tg"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !mgr.IsBindingDisabled("tg") {
		t.Fatal("expected binding disabled")
	}

	// Rebind (same adapter, exclusive) — must purge the stale tombstone.
	if err := mgr.BindAdapterToWorkspace("tg", "/tmp/test-workspace"); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	// The tombstone must be gone — the old persisted binding no longer exists.
	if mgr.IsBindingDisabled("tg") {
		t.Fatal("#689: stale disabledBindings tombstone survived rebind; EnableBinding would resurrect the pre-rebind copy (ghost)")
	}
	// And the fresh binding must be live in currentBindings (previously
	// reloadBindingLocked skipped it because of the surviving tombstone).
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

// Defect 2: UnmuteBinding must reject a binding owned by another session.
func TestIssue689_UnmuteOwnershipCheck(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "other-session", // foreign-mute: belongs to instance A
		Muted:         true,
	}
	mgr.mu.Unlock()

	if err := mgr.UnmuteBinding("tg"); err == nil {
		t.Fatal("#689: unmute of another session's binding succeeded — channel hijack via UpdateSessionID rewrite")
	}
}

// Defect 2 (companion): own binding still unmutes fine.
func TestIssue689_UnmuteOwnBindingStillWorks(t *testing.T) {
	mgr, _ := testDummyManager()
	mgr.mu.Lock()
	mgr.currentBindings["tg"] = &ChannelBinding{
		Adapter:       "tg",
		Workspace:     "/tmp/test-workspace",
		LastSessionID: "test-session", // matches testDummyManager session
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

// Defect 3: pre-session reload must preserve in-memory bindings.
func TestIssue689_ReloadWithoutSessionKeepsMemoryState(t *testing.T) {
	mgr := NewManager()
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	// No session bound — BindAdapterToWorkspace path reaches
	// reloadBindingLocked with m.session == nil.
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
