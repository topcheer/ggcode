package im

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestBindingWatcher_AutoMutesStaleOwnership verifies that when another instance
// changes the LastSessionID on a binding, the watcher auto-mutes the adapter.
func TestBindingWatcher_AutoMutesStaleOwnership(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")
	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{Workspace: "/ws1", SessionID: "session-A"})

	// Add a binding owned by session-A.
	binding := ChannelBinding{
		Workspace: "/ws1",
		Adapter:   "test-adapter",
		Platform:  "test",
		ChannelID: "ch1",
		BoundAt:   time.Now(),
	}
	binding.LastSessionID = "session-A"
	if _, err := mgr.BindChannel(binding); err != nil {
		t.Fatal(err)
	}

	// Start the watcher.
	mgr.StartBindingWatcher()
	defer mgr.StopBindingWatcher()

	// Verify the binding is active (not muted).
	if mgr.IsMuted("test-adapter") {
		t.Fatal("binding should not be muted initially")
	}

	// Simulate another instance claiming the binding.
	if err := store.UpdateSessionID("/ws1", "test-adapter", "session-B"); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to detect the change (poll interval is 3s, but
	// the file path check triggers immediately since mtime changed).
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout: binding was not auto-muted")
		case <-time.After(500 * time.Millisecond):
		}
		if mgr.IsMuted("test-adapter") {
			break
		}
	}

	// Verify it's muted.
	if !mgr.IsMuted("test-adapter") {
		t.Fatal("binding should be muted after session ownership changed")
	}
}

// TestBindingWatcher_NoMuteWhenSameSession verifies the watcher does NOT mute
// when the LastSessionID matches our session.
func TestBindingWatcher_NoMuteWhenSameSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")
	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{Workspace: "/ws1", SessionID: "session-A"})

	binding := ChannelBinding{
		Workspace: "/ws1",
		Adapter:   "test-adapter",
		Platform:  "test",
		ChannelID: "ch1",
		BoundAt:   time.Now(),
	}
	binding.LastSessionID = "session-A"
	mgr.BindChannel(binding)

	mgr.StartBindingWatcher()
	defer mgr.StopBindingWatcher()

	// Save the binding again with the same session ID (simulates a harmless
	// re-write by our own UnmuteBinding).
	store.Save(binding)

	time.Sleep(2 * time.Second)

	if mgr.IsMuted("test-adapter") {
		t.Fatal("binding should NOT be muted when session ID matches")
	}
}

// TestBindingWatcher_SkipsAlreadyMuted verifies the watcher does nothing
// when the binding is already muted.
func TestBindingWatcher_SkipsAlreadyMuted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")
	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{Workspace: "/ws1", SessionID: "session-A"})

	binding := ChannelBinding{
		Workspace: "/ws1",
		Adapter:   "test-adapter",
		Platform:  "test",
		ChannelID: "ch1",
		BoundAt:   time.Now(),
	}
	binding.LastSessionID = "session-A"
	mgr.BindChannel(binding)

	// Mute it manually first.
	mgr.MuteBinding("test-adapter")

	mgr.StartBindingWatcher()
	defer mgr.StopBindingWatcher()

	// Change session ID externally.
	store.UpdateSessionID("/ws1", "test-adapter", "session-B")

	time.Sleep(2 * time.Second)

	// Should still be muted (no error, no panic).
	if !mgr.IsMuted("test-adapter") {
		t.Fatal("binding should still be muted")
	}
}

// TestCheckBindingOwnership_MemoryStore tests the core logic without file I/O.
func TestCheckBindingOwnership_MemoryStore(t *testing.T) {
	store := NewMemoryBindingStore()
	mgr := NewManager()
	mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{Workspace: "/ws1", SessionID: "session-A"})

	binding := ChannelBinding{
		Workspace: "/ws1",
		Adapter:   "adapter1",
		Platform:  "test",
		ChannelID: "ch1",
		BoundAt:   time.Now(),
	}
	binding.LastSessionID = "session-A"
	mgr.BindChannel(binding)

	// Simulate another instance claiming it.
	store.UpdateSessionID("/ws1", "adapter1", "session-B")

	// Run the check directly (no polling needed).
	mgr.checkBindingOwnership(store, "/ws1", "session-A")

	if !mgr.IsMuted("adapter1") {
		t.Fatal("adapter1 should be muted after ownership changed to session-B")
	}
}

// TestBindingWatcher_RestartOnBindSession verifies that calling BindSession
// stops the old watcher and starts a new one with the updated session ID.
func TestBindingWatcher_RestartOnBindSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.json")
	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}

	mgr := NewManager()
	mgr.SetBindingStore(store)
	mgr.BindSession(SessionBinding{Workspace: "/ws1", SessionID: "session-A"})

	mgr.StartBindingWatcher()

	mgr.mu.RLock()
	firstCtx := mgr.bindingWatcherCtx
	mgr.mu.RUnlock()
	if firstCtx == nil {
		t.Fatal("watcher should be running")
	}

	// Re-bind with a different session.
	var wg sync.WaitGroup
	wg.Add(1)
	mgr.SetOnUpdate(func(_ StatusSnapshot) {
		wg.Done()
	})
	mgr.BindSession(SessionBinding{Workspace: "/ws1", SessionID: "session-A2"})
	wg.Wait()

	// The old watcher context should be cancelled.
	select {
	case <-firstCtx.Done():
		// Good — old watcher was cancelled.
	case <-time.After(2 * time.Second):
		t.Fatal("old watcher context was not cancelled")
	}

	// A new watcher should be running.
	mgr.mu.RLock()
	secondCancel := mgr.bindingWatcherCancel
	mgr.mu.RUnlock()
	if secondCancel == nil {
		t.Fatal("new watcher should be running after BindSession")
	}

	mgr.StopBindingWatcher()

	mgr.SetOnUpdate(nil)     // cleanup
	_ = context.Background() // keep import
}
