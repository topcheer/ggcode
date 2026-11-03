package im

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBindingWatcher_AutoMutesStaleOwnership(t *testing.T) {
	origInterval := bindingWatcherInterval
	bindingWatcherInterval = 100 * time.Millisecond
	t.Cleanup(func() { bindingWatcherInterval = origInterval })

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
	if _, err := mgr.BindChannel(binding); err != nil {
		t.Fatal(err)
	}

	mgr.StartBindingWatcher()
	defer mgr.StopBindingWatcher()

	if mgr.IsMuted("test-adapter") {
		t.Fatal("binding should not be muted initially")
	}

	if err := store.UpdateSessionID("/ws1", "test-adapter", "session-B"); err != nil {
		t.Fatal(err)
	}

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

	if !mgr.IsMuted("test-adapter") {
		t.Fatal("binding should be muted after session ownership changed")
	}
}

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

	origInterval := bindingWatcherInterval
	bindingWatcherInterval = 100 * time.Millisecond
	t.Cleanup(func() { bindingWatcherInterval = origInterval })

	mgr.StartBindingWatcher()
	defer mgr.StopBindingWatcher()

	store.Save(binding)

	time.Sleep(500 * time.Millisecond)

	if mgr.IsMuted("test-adapter") {
		t.Fatal("binding should NOT be muted when session ID matches")
	}
}

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

	mgr.MuteBinding("test-adapter")

	origInterval := bindingWatcherInterval
	bindingWatcherInterval = 100 * time.Millisecond
	t.Cleanup(func() { bindingWatcherInterval = origInterval })

	mgr.StartBindingWatcher()
	defer mgr.StopBindingWatcher()

	store.UpdateSessionID("/ws1", "test-adapter", "session-B")

	time.Sleep(500 * time.Millisecond)

	if !mgr.IsMuted("test-adapter") {
		t.Fatal("binding should still be muted")
	}
}

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

	store.UpdateSessionID("/ws1", "adapter1", "session-B")

	mgr.checkBindingOwnership(store, "/ws1", "session-A")

	if !mgr.IsMuted("adapter1") {
		t.Fatal("adapter1 should be muted after ownership changed to session-B")
	}
}

func TestBindingWatcher_RemovesOnCrossWorkspaceTakeover(t *testing.T) {
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

	mgr.mu.RLock()
	_, exists := mgr.currentBindings["adapter1"]
	mgr.mu.RUnlock()
	if !exists {
		t.Fatal("adapter1 should exist in currentBindings before takeover")
	}

	if err := store.Delete("/ws1", "adapter1"); err != nil {
		t.Fatal(err)
	}

	mgr.checkBindingOwnership(store, "/ws1", "session-A")

	mgr.mu.RLock()
	_, exists = mgr.currentBindings["adapter1"]
	mgr.mu.RUnlock()
	if exists {
		t.Fatal("adapter1 should be removed from currentBindings after cross-workspace takeover")
	}
}

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

	var wg sync.WaitGroup
	wg.Add(1)
	mgr.SetOnUpdate(func(_ StatusSnapshot) {
		wg.Done()
	})
	mgr.BindSession(SessionBinding{Workspace: "/ws1", SessionID: "session-A2"})
	wg.Wait()

	select {
	case <-firstCtx.Done():

	case <-time.After(2 * time.Second):
		t.Fatal("old watcher context was not cancelled")
	}

	mgr.mu.RLock()
	secondCancel := mgr.bindingWatcherCancel
	mgr.mu.RUnlock()
	if secondCancel == nil {
		t.Fatal("new watcher should be running after BindSession")
	}

	mgr.StopBindingWatcher()

	mgr.SetOnUpdate(nil)
	_ = context.Background()
}
