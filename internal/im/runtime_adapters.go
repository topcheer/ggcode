package im

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

func (m *Manager) RegisterSink(sink Sink) {
	if sink == nil {
		return
	}
	m.mu.Lock()
	m.sinks[sink.Name()] = sink
	m.mu.Unlock()
}

func (m *Manager) UnregisterSink(name string) {
	m.mu.Lock()
	delete(m.sinks, name)
	m.mu.Unlock()
}

// RegisterAdapterCancel registers a cancel function for an adapter.
// When the adapter is muted or disabled, the cancel is called to stop
// its goroutine and drop the connection.
func (m *Manager) RegisterAdapterCancel(adapterName string, cancel context.CancelFunc) {
	m.mu.Lock()
	m.adapterCancels[adapterName] = cancel
	m.mu.Unlock()
}

// stopAdapter cancels an adapter's context and unregisters its sink.
func (m *Manager) stopAdapter(adapterName string) {
	debug.Log("im", "stopAdapter: name=%s", adapterName)

	if cancel, ok := m.adapterCancels[adapterName]; ok && cancel != nil {
		debug.Log("im", "stopAdapter: cancelling context for %s", adapterName)
		cancel()
		delete(m.adapterCancels, adapterName)
	} else {
		debug.Log("im", "stopAdapter: no cancel func for %s", adapterName)
	}

	if sink, ok := m.sinks[adapterName]; ok {
		if closer, ok := sink.(Closer); ok {
			debug.Log("im", "stopAdapter: calling Close() on %s", adapterName)
			if err := closer.Close(); err != nil {
				debug.Log("im", "stopAdapter: Close() error for %s: %v", adapterName, err)
			}
		} else {
			debug.Log("im", "stopAdapter: %s does not implement Closer", adapterName)
		}
	} else {
		debug.Log("im", "stopAdapter: no sink registered for %s", adapterName)
	}
	delete(m.sinks, adapterName)

	if state, ok := m.adapters[adapterName]; ok {
		state.Healthy = false
		state.Status = "disconnected"
		state.LastError = ""
		state.UpdatedAt = time.Now()
		m.adapters[adapterName] = state
	}
	debug.Log("im", "stopAdapter: done for %s", adapterName)
}

// StopAdapter stops a running adapter by name. This is the public version
// of stopAdapter, used for hot-reloading config changes without altering
// binding state.
func (m *Manager) StopAdapter(adapterName string) {
	m.mu.Lock()
	m.stopAdapter(adapterName)
	m.mu.Unlock()
}

// persistBinding saves a binding to the store, stripping the Muted flag
// since Muted is an in-memory-only state that must not be persisted.
func (m *Manager) persistBinding(b ChannelBinding) error {
	if m.bindingStore == nil {
		return nil
	}
	b.Muted = false
	return m.bindingStore.Save(b)
}

func (m *Manager) GenerateShareLink(ctx context.Context, adapter, callbackData string) (string, error) {
	m.mu.RLock()
	sink := m.sinks[strings.TrimSpace(adapter)]
	m.mu.RUnlock()
	if sink == nil {
		return "", fmt.Errorf("IM adapter %q is not running", strings.TrimSpace(adapter))
	}
	provider, ok := sink.(ShareLinkProvider)
	if !ok {
		return "", fmt.Errorf("IM adapter %q does not support share links", strings.TrimSpace(adapter))
	}
	return provider.GenerateShareLink(ctx, callbackData)
}

func (m *Manager) PublishAdapterState(state AdapterState) {
	m.mu.Lock()

	if b, ok := m.currentBindings[state.Name]; ok && b.Muted {
		m.mu.Unlock()
		return
	}

	if existing, ok := m.adapters[state.Name]; ok && existing.Status == "disconnected" && state.Status != "connected" {
		m.mu.Unlock()
		debug.Log("im", "PublishAdapterState: ignoring late state update for %s (already disconnected)", state.Name)
		return
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}
	m.adapters[state.Name] = state
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
}
