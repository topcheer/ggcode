package im

import (
	"context"
	"errors"
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

func (m *Manager) UpdateBindingContextToken(adapter, token string) {
	m.mu.Lock()
	for _, b := range m.currentBindings {
		if b.Adapter == adapter {
			b.ContextToken = token
			b.ContextTokenUpdatedAt = time.Now()
			if m.bindingStore != nil {

				if err := m.bindingStore.Save(*b); err != nil {
					debug.Log("wechat", "persist context_token failed: %v", err)
				}
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

	for name, b := range m.currentBindings {
		if normalizeWorkspace(b.Workspace) == workspace {
			m.stopAdapter(name)
		}
	}
	for name, b := range m.disabledBindings {
		if normalizeWorkspace(b.Workspace) == workspace {
			m.stopAdapter(name)
		}
	}

	for name, b := range m.currentBindings {
		if normalizeWorkspace(b.Workspace) == workspace {
			delete(m.currentBindings, name)
		}
	}

	for name, b := range m.disabledBindings {
		if normalizeWorkspace(b.Workspace) == workspace {
			delete(m.disabledBindings, name)
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

	m.stopAdapter(adapter)
	delete(m.currentBindings, adapter)

	delete(m.disabledBindings, adapter)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}

	m.syncInstanceActiveChannels()
	return nil
}

// BindAdapterToWorkspace binds an adapter to a specific workspace,
// removing any existing binding to a different workspace.
//
// #556 contract note: this method does NOT validate that adapterName exists
// in the user's config — that is the caller's responsibility. Binding before
// the adapter is registered/started is a SUPPORTED flow ("bound but not yet
// active; takes effect on next startup"), so a runtime-side existence check
// here would break legitimate deferred activation. The desktop entry point
// (desktop/wailskit/im.go BindIMAdapter/RebindIMAdapter) validates against
// config to reject ghost bindings.
func (m *Manager) BindAdapterToWorkspace(adapterName, workspace string) error {
	if adapterName == "" || workspace == "" {
		return fmt.Errorf("adapter name and workspace must not be empty")
	}

	workspace = normalizeWorkspace(workspace)

	m.mu.Lock()

	if m.bindingStore == nil {
		m.mu.Unlock()
		return fmt.Errorf("binding store not configured")
	}

	binding := ChannelBinding{
		Adapter:   adapterName,
		Workspace: workspace,
		BoundAt:   time.Now(),
	}

	if err := m.bindingStore.BindExclusive(binding); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("exclusive bind: %w", err)
	}

	delete(m.disabledBindings, adapterName)

	if err := m.reloadBindingLocked(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("reload bindings: %w", err)
	}

	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()

	if cb != nil {
		cb(snapshot)
	}

	m.syncInstanceActiveChannels()

	return nil
}

func (m *Manager) AdapterBindings(adapterName string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.bindingStore == nil {
		return nil
	}
	bindings, err := m.bindingStore.ListByAdapter(adapterName)
	if err != nil {
		return nil
	}
	var out []string
	for _, b := range bindings {
		out = append(out, b.Workspace)
	}
	return out
}

func (m *Manager) UnbindAdapter(adapterName string) error {
	m.mu.Lock()
	if m.bindingStore != nil {
		bindings, err := m.bindingStore.ListByAdapter(adapterName)
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

	m.stopAdapter(adapterName)
	delete(m.currentBindings, adapterName)

	delete(m.disabledBindings, adapterName)
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

func (m *Manager) DisableBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.currentBindings[adapterName]
	if !ok {
		m.mu.Unlock()
		return ErrNoChannelBound
	}

	switch m.bindingOwnershipLocked(binding) {
	case ownershipForeignLive:
		m.mu.Unlock()
		return fmt.Errorf("adapter %q is owned by another live session (last=%s); disable denied (disable it on the owning instance or rebind to take over)", adapterName, binding.LastSessionID)
	case ownershipForeignDead:
		debug.Log("im", "DisableBinding: dead-owner takeover of %s from dead session=%s", adapterName, binding.LastSessionID)
	}
	cp := *binding
	m.disabledBindings[adapterName] = &cp
	delete(m.currentBindings, adapterName)
	m.stopAdapter(adapterName)
	workspace := ""
	if m.session != nil {
		workspace = m.session.Workspace
	}
	store := m.bindingStore
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}

	if store != nil && workspace != "" {
		if err := store.UpdateSessionID(workspace, adapterName, ""); err != nil {
			debug.Log("im", "DisableBinding: failed to clear LastSessionID for %s: %v", adapterName, err)
		}
	}
	debug.Log("im", "DisableBinding: adapter=%s workspace=%s", adapterName, workspace)
	m.syncInstanceActiveChannels()
	return nil
}

func (m *Manager) EnableBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.disabledBindings[adapterName]
	if !ok {
		m.mu.Unlock()
		return ErrNoChannelBound
	}

	switch m.bindingOwnershipLocked(binding) {
	case ownershipForeignLive:
		m.mu.Unlock()
		return fmt.Errorf("adapter %q is owned by another live session (last=%s); enable denied (disable elsewhere first or rebind to take over)", adapterName, binding.LastSessionID)
	case ownershipForeignDead:
		debug.Log("im", "EnableBinding: dead-owner takeover of %s from dead session=%s", adapterName, binding.LastSessionID)
	}
	copy := *binding

	if m.session != nil {
		copy.LastSessionID = m.session.SessionID
	}
	m.currentBindings[adapterName] = &copy
	delete(m.disabledBindings, adapterName)
	onRestart := m.onRestart
	workspace := ""
	sessionID := ""
	if m.session != nil {
		workspace = m.session.Workspace
		sessionID = m.session.SessionID
	}
	store := m.bindingStore
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}

	if store != nil && workspace != "" {
		if err := store.UpdateSessionID(workspace, adapterName, sessionID); err != nil {

			debug.Log("im", "EnableBinding: failed to set LastSessionID for %s: %v", adapterName, err)
			m.syncInstanceActiveChannels()
			return fmt.Errorf("enable adapter %s: ownership claim failed: %w", adapterName, err)
		}
	}
	if onRestart != nil {
		if err := onRestart(adapterName); err != nil {
			debug.Log("im", "enable restart adapter %s: %v", adapterName, err)

			m.syncInstanceActiveChannels()
			return fmt.Errorf("enable adapter %s: restart failed: %w", adapterName, err)
		}
	}
	debug.Log("im", "EnableBinding: adapter=%s workspace=%s", adapterName, workspace)
	m.syncInstanceActiveChannels()
	return nil
}

func (m *Manager) DisabledBindings() []ChannelBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChannelBinding, 0, len(m.disabledBindings))
	for _, b := range m.disabledBindings {
		out = append(out, *b)
	}
	return out
}

func (m *Manager) IsBindingDisabled(adapterName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.disabledBindings[adapterName]
	return ok
}

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

	debug.Log("im", "MuteBinding: adapter=%s", adapterName)
	m.syncInstanceActiveChannels()
	return nil
}

func (m *Manager) UnmuteBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.currentBindings[adapterName]
	if !ok || !binding.Muted {
		m.mu.Unlock()
		return ErrNoChannelBound
	}

	switch m.bindingOwnershipLocked(binding) {
	case ownershipForeignLive:
		m.mu.Unlock()
		return fmt.Errorf("adapter %q is owned by another live session (last=%s); unmute denied (disable+enable the adapter or rebind to take over)", adapterName, binding.LastSessionID)
	case ownershipForeignDead:
		debug.Log("im", "UnmuteBinding: dead-owner takeover of %s from dead session=%s", adapterName, binding.LastSessionID)

		binding.LastSessionID = m.session.SessionID
	}
	binding.Muted = false
	onRestart := m.onRestart
	workspace := ""
	sessionID := ""
	if m.session != nil {
		workspace = m.session.Workspace
		sessionID = m.session.SessionID
	}
	store := m.bindingStore
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}

	if store != nil && workspace != "" {
		if err := store.UpdateSessionID(workspace, adapterName, sessionID); err != nil {

			debug.Log("im", "UnmuteBinding: failed to set LastSessionID for %s: %v", adapterName, err)
			m.syncInstanceActiveChannels()
			return fmt.Errorf("unmute adapter %s: ownership claim failed: %w", adapterName, err)
		}
	}
	if onRestart != nil {
		if err := onRestart(adapterName); err != nil {
			debug.Log("im", "unmute restart adapter %s: %v", adapterName, err)

			m.syncInstanceActiveChannels()
			return fmt.Errorf("unmute adapter %s: restart failed: %w", adapterName, err)
		}
	}
	debug.Log("im", "UnmuteBinding: adapter=%s workspace=%s", adapterName, workspace)
	m.syncInstanceActiveChannels()
	return nil
}

func (m *Manager) MuteAll() (int, error) {
	return m.MuteAllExcept("")
}

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

type bindingOwnership int

const (
	ownershipOwned bindingOwnership = iota
	ownershipForeignDead
	ownershipForeignLive
)

func (m *Manager) bindingOwnershipLocked(binding *ChannelBinding) bindingOwnership {
	if m.session == nil || binding.LastSessionID == "" || binding.LastSessionID == m.session.SessionID {
		return ownershipOwned
	}
	if m.foreignOwnerPossiblyAliveLocked() {
		return ownershipForeignLive
	}
	return ownershipForeignDead
}

func (m *Manager) foreignOwnerPossiblyAliveLocked() bool {
	if m.instanceDetect == nil {
		return true
	}
	return len(m.instanceDetect.ListInstances()) > 1
}

func (m *Manager) UnmuteAll() (int, error) {
	m.mu.Lock()
	var toRestart []string
	var toClaim []string
	count := 0
	for name, binding := range m.currentBindings {
		if !binding.Muted {
			continue
		}
		switch m.bindingOwnershipLocked(binding) {
		case ownershipForeignLive:

			debug.Log("im", "UnmuteAll: skipping %s owned by live session=%s", name, binding.LastSessionID)
			continue
		case ownershipForeignDead:
			binding.LastSessionID = m.session.SessionID
			toClaim = append(toClaim, name)
		}
		binding.Muted = false
		toRestart = append(toRestart, name)
		count++
	}
	onRestart := m.onRestart
	workspace := ""
	sessionID := ""
	if m.session != nil {
		workspace = m.session.Workspace
		sessionID = m.session.SessionID
	}
	store := m.bindingStore
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	for _, name := range toClaim {
		if store != nil && workspace != "" {
			if err := store.UpdateSessionID(workspace, name, sessionID); err != nil {
				debug.Log("im", "UnmuteAll: failed to claim %s for session=%s: %v", name, sessionID, err)
			}
		}
	}
	var restartErrs []error
	for _, name := range toRestart {
		if onRestart != nil {
			if err := onRestart(name); err != nil {
				debug.Log("im", "unmute-all restart adapter %s: %v", name, err)

				restartErrs = append(restartErrs, fmt.Errorf("adapter %s: %w", name, err))
			}
		}
	}
	m.syncInstanceActiveChannels()
	if len(restartErrs) > 0 {
		return count, fmt.Errorf("unmute-all restart failed: %w", errors.Join(restartErrs...))
	}
	return count, nil
}

func (m *Manager) DisableAll() (int, error) {
	m.mu.Lock()
	count := 0
	var names []string
	for name, binding := range m.currentBindings {
		if binding.Muted {

			continue
		}
		switch m.bindingOwnershipLocked(binding) {
		case ownershipForeignLive:
			debug.Log("im", "DisableAll: skipping %s owned by live session=%s", name, binding.LastSessionID)
			continue
		case ownershipForeignDead:
			debug.Log("im", "DisableAll: dead-owner takeover of %s from dead session=%s", name, binding.LastSessionID)
		}
		cp := *binding
		m.disabledBindings[name] = &cp
		delete(m.currentBindings, name)
		m.stopAdapter(name)
		names = append(names, name)
		count++
	}
	workspace := ""
	if m.session != nil {
		workspace = m.session.Workspace
	}
	store := m.bindingStore
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}

	if store != nil && workspace != "" {
		for _, name := range names {
			if err := store.UpdateSessionID(workspace, name, ""); err != nil {
				debug.Log("im", "DisableAll: failed to clear LastSessionID for %s: %v", name, err)
			}
		}
	}
	debug.Log("im", "DisableAll: disabled %d adapters: %v", count, names)

	m.syncInstanceActiveChannels()
	return count, nil
}

func (m *Manager) EnableAll() (int, error) {
	m.mu.Lock()
	var toRestart []string
	var toClaim []string
	var skipped []string
	count := 0
	for name, binding := range m.disabledBindings {
		switch m.bindingOwnershipLocked(binding) {
		case ownershipForeignLive:

			debug.Log("im", "EnableAll: skipping %s owned by live session=%s", name, binding.LastSessionID)
			skipped = append(skipped, name)
			continue
		case ownershipForeignDead:
			binding.LastSessionID = m.session.SessionID
		}
		copy := *binding
		if m.session != nil {
			copy.LastSessionID = m.session.SessionID
		}
		m.currentBindings[name] = &copy
		delete(m.disabledBindings, name)
		toRestart = append(toRestart, name)
		toClaim = append(toClaim, name)
		count++
	}
	onRestart := m.onRestart
	workspace := ""
	sessionID := ""
	if m.session != nil {
		workspace = m.session.Workspace
		sessionID = m.session.SessionID
	}
	store := m.bindingStore
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}

	for _, name := range toClaim {
		if store != nil && workspace != "" {
			if err := store.UpdateSessionID(workspace, name, sessionID); err != nil {
				debug.Log("im", "EnableAll: failed to set LastSessionID for %s: %v", name, err)
			}
		}
	}
	var restartErrs []error
	for _, name := range toRestart {
		if onRestart != nil {
			if err := onRestart(name); err != nil {
				debug.Log("im", "enable-all restart adapter %s: %v", name, err)

				restartErrs = append(restartErrs, fmt.Errorf("adapter %s: %w", name, err))
			}
		}
	}
	_ = skipped

	m.syncInstanceActiveChannels()
	if len(restartErrs) > 0 {
		return count, fmt.Errorf("enable-all restart failed: %w", errors.Join(restartErrs...))
	}
	return count, nil
}

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

func (m *Manager) ReservePassiveSeqs(workspace, messageID string, n int) (int, error) {
	if n < 1 {
		return 0, fmt.Errorf("reserving %d passive seqs: n must be >= 1", n)
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return 0, ErrNoChannelBound
	}
	if reserver, ok := m.bindingStore.(PassiveSeqReserver); ok {
		start, err := reserver.ReservePassiveSeqs(workspace, messageID, n)
		if err != nil {
			return 0, err
		}

		m.mu.Lock()
		for k, b := range m.currentBindings {
			if normalizeWorkspace(b.Workspace) == normalizeWorkspace(workspace) &&
				strings.TrimSpace(b.LastInboundMessageID) == messageID && b.PassiveReplyCount < start+n-1 {
				b.PassiveReplyCount = start + n - 1
				if b.PassiveReplyStartedAt.IsZero() {
					b.PassiveReplyStartedAt = time.Now()
				}
				m.currentBindings[k] = b
			}
		}
		snapshot, cb := m.snapshotAndCallbackLocked()
		m.mu.Unlock()
		if cb != nil {
			cb(snapshot)
		}
		return start, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.currentBindings {
		if normalizeWorkspace(b.Workspace) != normalizeWorkspace(workspace) ||
			strings.TrimSpace(b.LastInboundMessageID) != messageID {
			continue
		}
		if b.PassiveReplyStartedAt.IsZero() {
			b.PassiveReplyStartedAt = time.Now()
		}
		start := b.PassiveReplyCount + 1
		b.PassiveReplyCount += n
		if m.bindingStore != nil {
			if err := m.persistBinding(*b); err != nil {

				b.PassiveReplyCount = start - 1
				return 0, err
			}
		}
		return start, nil
	}
	return 0, ErrNoChannelBound
}

func (m *Manager) RecordPassiveReply(workspace, messageID string, sentAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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
		found = true
		if err := m.recordPassiveReplyLocked(b, sentAt); err != nil {
			return err
		}
		break
	}
	if !found {
		return ErrNoChannelBound
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

func (m *Manager) RecordPassiveReplyByMessage(messageID string, sentAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	messageID = strings.TrimSpace(messageID)
	var found bool
	for _, b := range m.currentBindings {
		if messageID == "" || strings.TrimSpace(b.LastInboundMessageID) != messageID {
			continue
		}
		found = true
		if err := m.recordPassiveReplyLocked(b, sentAt); err != nil {
			return err
		}
		break
	}
	if !found {
		return ErrNoChannelBound
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

func (m *Manager) recordPassiveReplyLocked(b *ChannelBinding, sentAt time.Time) error {
	if sentAt.IsZero() {
		sentAt = time.Now()
	}
	if b.PassiveReplyStartedAt.IsZero() {
		b.PassiveReplyStartedAt = sentAt
	}

	oldCount, oldStart := b.PassiveReplyCount, b.PassiveReplyStartedAt
	b.PassiveReplyCount++
	if m.bindingStore != nil {
		if err := m.persistBinding(*b); err != nil {
			b.PassiveReplyCount = oldCount
			b.PassiveReplyStartedAt = oldStart
			return err
		}
	}
	return nil
}

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
