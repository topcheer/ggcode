package im

import (
	"context"
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

var bindingWatcherInterval = 3 * time.Second

func bindingStatChanged(mod time.Time, size int64, lastMod time.Time, lastSize int64) bool {
	if mod.After(lastMod) {
		return true
	}

	return !mod.Before(lastMod) && size != lastSize
}

func (m *Manager) StartBindingWatcher() {
	m.mu.Lock()
	if m.bindingWatcherCancel != nil {
		m.mu.Unlock()
		return
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

	interval := bindingWatcherInterval

	safego.Go("im.binding-watcher", func() {
		var lastMod time.Time
		var lastSize int64

		var bindPath string
		if fps, ok := store.(*JSONFileBindingStore); ok {
			bindPath = fps.path
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				debug.Log("im", "binding watcher: stopped for session=%s", sessionID)
				return
			case <-ticker.C:
				if bindPath != "" {

					fi, err := os.Stat(bindPath)
					if err != nil {
						continue
					}
					if !bindingStatChanged(fi.ModTime(), fi.Size(), lastMod, lastSize) {
						continue
					}
					lastMod = fi.ModTime()
					lastSize = fi.Size()
				}

				m.checkBindingOwnership(store, workspace, sessionID)
			}
		}
	})
}

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

func (m *Manager) checkBindingOwnership(store BindingStore, workspace, sessionID string) {
	bindings, err := store.ListByWorkspace(workspace)
	if err != nil {
		debug.Log("im", "binding watcher: ListByWorkspace error: %v", err)
		return
	}

	persistedSessionIDs := make(map[string]string, len(bindings))
	for _, b := range bindings {
		persistedSessionIDs[b.Adapter] = b.LastSessionID
	}

	m.mu.Lock()
	var toMute []string
	var toRemove []string
	for name, binding := range m.currentBindings {
		if binding.Muted {
			continue
		}
		persistedSID, exists := persistedSessionIDs[name]
		if !exists {

			toRemove = append(toRemove, name)
			continue
		}

		if persistedSID != "" && persistedSID != sessionID {
			toMute = append(toMute, name)
		}
	}

	for _, name := range toRemove {
		binding, ok := m.currentBindings[name]
		if !ok {
			continue
		}
		debug.Log("im", "binding watcher: removing %s - binding deleted from workspace (taken by another workspace)", name)
		binding.Muted = true
		m.stopAdapter(name)
		delete(m.currentBindings, name)
	}

	for _, name := range toMute {
		if binding, ok := m.currentBindings[name]; ok {
			binding.Muted = true
			m.stopAdapter(name)
			debug.Log("im", "binding watcher: auto-muted %s - session ownership changed from %s to %s",
				name, sessionID, persistedSessionIDs[name])
		}
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()

	if len(toMute) > 0 || len(toRemove) > 0 {
		if cb != nil {
			cb(snapshot)
		}
		m.syncInstanceActiveChannels()
	}
}
