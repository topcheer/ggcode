package im

import (
	"context"
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// bindingWatcherInterval is how often the watcher polls the bindings file.
const bindingWatcherInterval = 3 * time.Second

// StartBindingWatcher starts a background goroutine that monitors the
// im-bindings.json file for LastSessionID changes by other ggcode instances.
//
// When another instance claims a binding (via UnmuteBinding/EnableBinding),
// it writes its own session ID as LastSessionID to the shared file. This
// watcher detects that change and auto-mutes the affected adapter so the
// original (now stale) instance stops competing for the channel.
//
// The watcher is a no-op for bindings that are already muted or disabled.
// It only acts on active (non-muted) bindings whose persisted LastSessionID
// no longer matches this instance's session.
//
// Call StopBindingWatcher to stop the goroutine (e.g. on shutdown).
func (m *Manager) StartBindingWatcher() {
	m.mu.Lock()
	if m.bindingWatcherCancel != nil {
		m.mu.Unlock()
		return // already running
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.bindingWatcherCtx = ctx
	m.bindingWatcherCancel = cancel
	store := m.bindingStore
	sessionID := ""
	workspace := ""
	if m.session != nil {
		sessionID = m.session.SessionID
		workspace = m.session.Workspace
	}
	m.mu.Unlock()

	if store == nil || workspace == "" || sessionID == "" {
		debug.Log("im", "binding watcher: not starting (store=%v workspace=%q sessionID=%q)",
			store != nil, workspace, sessionID)
		return
	}

	debug.Log("im", "binding watcher: started for session=%s workspace=%s", sessionID, workspace)

	safego.Go("im.binding-watcher", func() {
		var lastMod time.Time

		// Resolve the file path from the store (only JSONFileBindingStore has a path).
		var bindPath string
		if fps, ok := store.(*JSONFileBindingStore); ok {
			bindPath = fps.path
		}

		ticker := time.NewTicker(bindingWatcherInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				debug.Log("im", "binding watcher: stopped for session=%s", sessionID)
				return
			case <-ticker.C:
				if bindPath != "" {
					// Fast path: check mtime first. Only read the file if it changed.
					fi, err := os.Stat(bindPath)
					if err != nil {
						continue
					}
					if !fi.ModTime().After(lastMod) {
						continue // file not modified since last check
					}
					lastMod = fi.ModTime()
				}
				// Either no file path (MemoryBindingStore) or file was modified.
				m.checkBindingOwnership(store, workspace, sessionID)
			}
		}
	})
}

// StopBindingWatcher stops the binding watcher goroutine.
func (m *Manager) StopBindingWatcher() {
	m.mu.Lock()
	cancel := m.bindingWatcherCancel
	m.bindingWatcherCancel = nil
	m.bindingWatcherCtx = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// checkBindingOwnership reads the current persisted bindings and mutes any
// active adapter whose LastSessionID no longer matches this session.
func (m *Manager) checkBindingOwnership(store BindingStore, workspace, sessionID string) {
	bindings, err := store.ListByWorkspace(workspace)
	if err != nil {
		debug.Log("im", "binding watcher: ListByWorkspace error: %v", err)
		return
	}

	// Build a map of persisted LastSessionID per adapter.
	persistedSessionIDs := make(map[string]string, len(bindings))
	for _, b := range bindings {
		persistedSessionIDs[b.Adapter] = b.LastSessionID
	}

	m.mu.Lock()
	var toMute []string
	for name, binding := range m.currentBindings {
		if binding.Muted {
			continue // already muted
		}
		persistedSID, exists := persistedSessionIDs[name]
		if !exists {
			continue // binding deleted from store, skip
		}
		// If the persisted LastSessionID is non-empty and differs from our session,
		// another instance has claimed this binding.
		if persistedSID != "" && persistedSID != sessionID {
			toMute = append(toMute, name)
		}
	}
	// Mute the stale bindings while holding the lock.
	for _, name := range toMute {
		if binding, ok := m.currentBindings[name]; ok {
			binding.Muted = true
			m.stopAdapter(name)
			debug.Log("im", "binding watcher: auto-muted %s — session ownership changed from %s to %s",
				name, sessionID, persistedSessionIDs[name])
		}
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()

	if len(toMute) > 0 {
		if cb != nil {
			cb(snapshot)
		}
		m.syncInstanceActiveChannels()
	}
}
