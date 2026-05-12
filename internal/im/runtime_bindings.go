package im

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

func (m *Manager) BindChannel(binding ChannelBinding) (ChannelBinding, error) {
	m.mu.Lock()
	if m.session == nil {
		m.mu.Unlock()
		return ChannelBinding{}, ErrNoSessionBound
	}
	bound, err := m.bindChannelLocked(binding)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if err != nil {
		return ChannelBinding{}, err
	}
	if cb != nil {
		cb(snapshot)
	}
	m.syncInstanceActiveChannels()
	return bound, nil
}

// GetBindingContextToken returns the persisted ContextToken for the given adapter.
func (m *Manager) GetBindingContextToken(adapter string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.currentBindings {
		if b.Adapter == adapter {
			return b.ContextToken
		}
	}
	return ""
}

// UpdateBindingContextToken updates the ContextToken (and ContextTokenUpdatedAt) on the
// binding for the given adapter. The token is persisted to disk so it survives restarts.
// WeChat iLink requires context_token for every sendmessage; without it only ~2 messages
// succeed before the server stops responding. Tokens expire after ~24 hours.
func (m *Manager) UpdateBindingContextToken(adapter, token string) {
	m.mu.Lock()
	for _, b := range m.currentBindings {
		if b.Adapter == adapter {
			b.ContextToken = token
			b.ContextTokenUpdatedAt = time.Now()
			if m.bindingStore != nil {
				_ = m.bindingStore.Save(*b)
			}
			debug.Log("wechat", "persisted context_token for adapter=%s len=%d", adapter, len(token))
			break
		}
	}
	m.mu.Unlock()
}

func (m *Manager) UnbindChannel(workspace string) error {
	m.mu.Lock()
	if workspace == "" && m.session != nil {
		workspace = m.session.Workspace
	}
	workspace = normalizeWorkspace(workspace)
	// Delete all bindings for this workspace
	if m.bindingStore != nil {
		bindings, err := m.bindingStore.ListByWorkspace(workspace)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		for _, b := range bindings {
			if err := m.bindingStore.Delete(b.Workspace, b.Adapter); err != nil {
				m.mu.Unlock()
				return err
			}
		}
	}
	// Clear matching entries from currentBindings
	for name, b := range m.currentBindings {
		if normalizeWorkspace(b.Workspace) == workspace {
			delete(m.currentBindings, name)
		}
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	m.syncInstanceActiveChannels()
	return nil
}

// DeleteBinding removes a specific persisted binding by adapter and workspace.
func (m *Manager) DeleteBinding(adapter, workspace string) error {
	m.mu.Lock()
	if m.bindingStore == nil {
		m.mu.Unlock()
		return fmt.Errorf("no binding store")
	}
	workspace = normalizeWorkspace(workspace)
	if err := m.bindingStore.Delete(workspace, adapter); err != nil {
		m.mu.Unlock()
		return err
	}
	delete(m.currentBindings, adapter)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

// UnbindAdapter removes the binding for whatever workspace has the given
// adapter name. This is needed when unbinding from a panel where the current
// session workspace may differ from the workspace that originally bound the
// adapter. Returns ErrNoChannelBound if no binding uses this adapter.
func (m *Manager) UnbindAdapter(adapterName string) error {
	m.mu.Lock()
	if m.bindingStore != nil {
		bindings, err := m.bindingStore.ListByAdapter(adapterName)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		if len(bindings) == 0 {
			m.mu.Unlock()
			return ErrNoChannelBound
		}
		for _, b := range bindings {
			if err := m.bindingStore.Delete(b.Workspace, b.Adapter); err != nil {
				m.mu.Unlock()
				return err
			}
		}
	}
	delete(m.currentBindings, adapterName)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	m.syncInstanceActiveChannels()
	return nil
}

func (m *Manager) ClearChannelByAdapter(adapterName string) error {
	m.mu.Lock()
	if m.bindingStore != nil {
		bindings, err := m.bindingStore.ListByAdapter(adapterName)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		if len(bindings) == 0 {
			m.mu.Unlock()
			return ErrNoChannelBound
		}
		for _, b := range bindings {
			b.ChannelID = ""
			b.ThreadID = ""
			b.LastInboundMessageID = ""
			b.LastInboundAt = time.Time{}
			b.PassiveReplyCount = 0
			b.PassiveReplyStartedAt = time.Time{}
			if err := m.persistBinding(b); err != nil {
				m.mu.Unlock()
				return err
			}
		}
	}
	if b, ok := m.currentBindings[adapterName]; ok {
		b.ChannelID = ""
		b.ThreadID = ""
		b.LastInboundMessageID = ""
		b.LastInboundAt = time.Time{}
		b.PassiveReplyCount = 0
		b.PassiveReplyStartedAt = time.Time{}
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

// DisableBinding temporarily disables an adapter's binding for the current session.
// The binding is moved from currentBindings to disabledBindings, so it will no
// longer receive outbound messages and inbound messages will be rejected.
// The persistent binding is NOT deleted, so it can be re-enabled later.
func (m *Manager) DisableBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.currentBindings[adapterName]
	if !ok {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	cp := *binding
	m.disabledBindings[adapterName] = &cp
	delete(m.currentBindings, adapterName)
	m.stopAdapter(adapterName)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	m.syncInstanceActiveChannels()
	return nil
}

// EnableBinding re-enables a previously disabled adapter binding.
// The binding is moved back to currentBindings so it resumes receiving messages.
func (m *Manager) EnableBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.disabledBindings[adapterName]
	if !ok {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	copy := *binding
	m.currentBindings[adapterName] = &copy
	delete(m.disabledBindings, adapterName)
	onRestart := m.onRestart
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	if onRestart != nil {
		if err := onRestart(adapterName); err != nil {
			debug.Log("im", "enable restart adapter %s: %v", adapterName, err)
		}
	}
	m.syncInstanceActiveChannels()
	return nil
}

// DisabledBindings returns a snapshot of currently disabled bindings.
func (m *Manager) DisabledBindings() []ChannelBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChannelBinding, 0, len(m.disabledBindings))
	for _, b := range m.disabledBindings {
		out = append(out, *b)
	}
	return out
}

// IsBindingDisabled returns true if the given adapter's binding is currently disabled.
func (m *Manager) IsBindingDisabled(adapterName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.disabledBindings[adapterName]
	return ok
}

// --- Mute (in-memory, not persisted) ---

// MuteBinding mutes an adapter for this process only. The binding stays in
// currentBindings (so the UI still shows it as bound) but is marked Muted.
// The connection is dropped so inbound/outbound messages stop.
func (m *Manager) MuteBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.currentBindings[adapterName]
	if !ok {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	if binding.Muted {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	binding.Muted = true
	m.stopAdapter(adapterName)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	m.syncInstanceActiveChannels()
	return nil
}

// UnmuteBinding unmutes a previously muted adapter and restarts it.
func (m *Manager) UnmuteBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.currentBindings[adapterName]
	if !ok || !binding.Muted {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	binding.Muted = false
	onRestart := m.onRestart
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	if onRestart != nil {
		if err := onRestart(adapterName); err != nil {
			debug.Log("im", "unmute restart adapter %s: %v", adapterName, err)
		}
	}
	m.syncInstanceActiveChannels()
	return nil
}

// MuteAll mutes all currently active bindings for this process.
// Returns the number of adapters that were muted.
func (m *Manager) MuteAll() (int, error) {
	return m.MuteAllExcept("")
}

// MuteAllExcept mutes all currently active bindings except the named adapter.
// If exclude is empty, all bindings are muted. Returns the number muted.
func (m *Manager) MuteAllExcept(exclude string) (int, error) {
	m.mu.Lock()
	count := 0
	for name, binding := range m.currentBindings {
		if binding.Muted {
			continue
		}
		if name == exclude {
			continue
		}
		binding.Muted = true
		m.stopAdapter(name)
		count++
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	m.syncInstanceActiveChannels()
	return count, nil
}

// UnmuteAll unmutes all muted bindings for this process.
// Returns the number of adapters that were unmuted.
func (m *Manager) UnmuteAll() (int, error) {
	m.mu.Lock()
	var toRestart []string
	count := 0
	for name, binding := range m.currentBindings {
		if !binding.Muted {
			continue
		}
		binding.Muted = false
		toRestart = append(toRestart, name)
		count++
	}
	onRestart := m.onRestart
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	for _, name := range toRestart {
		if onRestart != nil {
			if err := onRestart(name); err != nil {
				debug.Log("im", "unmute-all restart adapter %s: %v", name, err)
			}
		}
	}
	m.syncInstanceActiveChannels()
	return count, nil
}

// DisableAll disables all active (non-muted, non-disabled) bindings.
func (m *Manager) DisableAll() (int, error) {
	m.mu.Lock()
	count := 0
	var names []string
	for name, binding := range m.currentBindings {
		cp := *binding
		m.disabledBindings[name] = &cp
		delete(m.currentBindings, name)
		m.stopAdapter(name)
		names = append(names, name)
		count++
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	debug.Log("im", "DisableAll: disabled %d adapters: %v", count, names)
	return count, nil
}

// EnableAll re-enables all disabled bindings.
func (m *Manager) EnableAll() (int, error) {
	m.mu.Lock()
	var toRestart []string
	count := 0
	for name, binding := range m.disabledBindings {
		copy := *binding
		m.currentBindings[name] = &copy
		delete(m.disabledBindings, name)
		toRestart = append(toRestart, name)
		count++
	}
	onRestart := m.onRestart
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	for _, name := range toRestart {
		if onRestart != nil {
			if err := onRestart(name); err != nil {
				debug.Log("im", "enable-all restart adapter %s: %v", name, err)
			}
		}
	}
	return count, nil
}

// ApplyAdapterConfig moves adapters marked as disabled in config from
// currentBindings to disabledBindings. Call this after BindSession and
// reloadBindingLocked during startup.
func (m *Manager) ApplyAdapterConfig(adapters map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, enabled := range adapters {
		if !enabled {
			if binding, ok := m.currentBindings[name]; ok {
				m.disabledBindings[name] = binding
				delete(m.currentBindings, name)
				m.stopAdapter(name)
			}
		}
	}
}

// MutedBindings returns a snapshot of currently muted bindings.
func (m *Manager) MutedBindings() []ChannelBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ChannelBinding
	for _, b := range m.currentBindings {
		if b.Muted {
			out = append(out, *b)
		}
	}
	return out
}

// IsBindingMuted returns true if the given adapter's binding is currently muted.
func (m *Manager) IsBindingMuted(adapterName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.currentBindings[adapterName]
	return ok && b.Muted
}

func (m *Manager) ClearChannel(workspace string) error {
	m.mu.Lock()
	if workspace == "" && m.session != nil {
		workspace = m.session.Workspace
	}
	workspace = normalizeWorkspace(workspace)
	var bindings []ChannelBinding
	for _, b := range m.currentBindings {
		if normalizeWorkspace(b.Workspace) == workspace {
			bindings = append(bindings, *b)
		}
	}
	if len(bindings) == 0 && m.bindingStore != nil {
		loaded, err := m.bindingStore.ListByWorkspace(workspace)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		bindings = loaded
	}
	if len(bindings) == 0 {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	for i := range bindings {
		bindings[i].ChannelID = ""
		bindings[i].ThreadID = ""
		bindings[i].LastInboundMessageID = ""
		bindings[i].LastInboundAt = time.Time{}
		bindings[i].PassiveReplyCount = 0
		bindings[i].PassiveReplyStartedAt = time.Time{}
		if m.bindingStore != nil {
			if err := m.persistBinding(bindings[i]); err != nil {
				m.mu.Unlock()
				return err
			}
		}
		if b, ok := m.currentBindings[bindings[i].Adapter]; ok && normalizeWorkspace(b.Workspace) == workspace {
			b.ChannelID = ""
			b.ThreadID = ""
			b.LastInboundMessageID = ""
			b.LastInboundAt = time.Time{}
			b.PassiveReplyCount = 0
			b.PassiveReplyStartedAt = time.Time{}
		}
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

func (m *Manager) ClearReplyWindow(workspace string) error {
	m.mu.Lock()
	if workspace == "" && m.session != nil {
		workspace = m.session.Workspace
	}
	workspace = normalizeWorkspace(workspace)
	var found bool
	for _, b := range m.currentBindings {
		if normalizeWorkspace(b.Workspace) == workspace {
			b.LastInboundMessageID = ""
			b.LastInboundAt = time.Time{}
			b.PassiveReplyCount = 0
			b.PassiveReplyStartedAt = time.Time{}
			found = true
			if m.bindingStore != nil {
				if err := m.persistBinding(*b); err != nil {
					m.mu.Unlock()
					return err
				}
			}
		}
	}
	if !found {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

func (m *Manager) SyncSessionHistory(ctx context.Context, binding ChannelBinding, messages []provider.Message) error {
	for _, event := range SessionHistoryEvents(messages) {
		if err := m.SendDirect(ctx, binding, event); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) RecordPassiveReply(workspace, messageID string, sentAt time.Time) error {
	m.mu.Lock()
	if workspace == "" && m.session != nil {
		workspace = m.session.Workspace
	}
	workspace = normalizeWorkspace(workspace)
	messageID = strings.TrimSpace(messageID)
	var found bool
	for _, b := range m.currentBindings {
		if normalizeWorkspace(b.Workspace) != workspace {
			continue
		}
		if messageID == "" || strings.TrimSpace(b.LastInboundMessageID) != messageID {
			continue
		}
		if sentAt.IsZero() {
			sentAt = time.Now()
		}
		if b.PassiveReplyStartedAt.IsZero() {
			b.PassiveReplyStartedAt = sentAt
		}
		b.PassiveReplyCount++
		if m.bindingStore != nil {
			if err := m.persistBinding(*b); err != nil {
				m.mu.Unlock()
				return err
			}
		}
		found = true
		break
	}
	if !found {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

// RecordOutboundMessage records the message ID of a bot reply so that
// typing indicator reactions can target it when no inbound message exists.
func (m *Manager) RecordOutboundMessage(workspace, adapter, messageID string) error {
	m.mu.Lock()
	workspace = normalizeWorkspace(workspace)
	messageID = strings.TrimSpace(messageID)
	adapter = strings.TrimSpace(adapter)
	if messageID == "" || adapter == "" {
		m.mu.Unlock()
		return nil
	}
	b, ok := m.currentBindings[adapter]
	if !ok || normalizeWorkspace(b.Workspace) != workspace {
		m.mu.Unlock()
		return nil
	}
	b.LastOutboundMessageID = messageID
	if m.bindingStore != nil {
		if err := m.persistBinding(*b); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	m.mu.Unlock()
	return nil
}

// TriggerTyping sends a typing indicator to all bound adapters that
// implement the TypingIndicator interface.
func (m *Manager) TriggerTyping(ctx context.Context) {
	m.mu.RLock()
	var targets []struct {
		binding ChannelBinding
		sink    TypingIndicator
	}
	for _, b := range m.currentBindings {
		if strings.TrimSpace(b.ChannelID) == "" {
			continue
		}
		sink := m.sinks[b.Adapter]
		if sink == nil {
			continue
		}
		ti, ok := sink.(TypingIndicator)
		if !ok {
			continue
		}
		targets = append(targets, struct {
			binding ChannelBinding
			sink    TypingIndicator
		}{binding: *b, sink: ti})
	}
	m.mu.RUnlock()
	for _, t := range targets {
		_ = t.sink.TriggerTyping(ctx, t.binding)
	}
}
