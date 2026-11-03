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

// UnbindAdapter removes the binding for whatever workspace has the given
// adapter name. This is needed when unbinding from a panel where the current
// session workspace may differ from the workspace that originally bound the
// AdapterBindings returns the workspaces the adapter is currently bound to
// (#1811 case 2: Rebind captures these so a failed rebind can restore the
// previous binding instead of leaving the adapter silently dead).
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

// adapter. Idempotent: no persisted binding for this adapter is a successful
// no-op, not an error (#396 cascade / #498 note — the old doc claimed
// ErrNoChannelBound here, contradicting the implementation below it).
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

// EnableBinding re-enables a previously disabled adapter binding.
// The binding is moved back to currentBindings so it resumes receiving messages.
// LastSessionID is set to the current session to claim ownership.
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

	debug.Log("im", "MuteBinding: adapter=%s", adapterName)
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

// bindingOwnership classifies a binding's persisted owner relative to this
// session. Shared by UnmuteBinding and UnmuteAll so both apply the identical
// #689/#693 rule (live foreign owner → reject/skip; dead foreign owner →
// takeover; own or unclaimed → proceed).
type bindingOwnership int

const (
	ownershipOwned       bindingOwnership = iota // ours or unclaimed
	ownershipForeignDead                         // foreign owner's instance is dead — takeover allowed
	ownershipForeignLive                         // foreign owner's instance is alive — reject/skip
)

// bindingOwnershipLocked classifies binding ownership. Caller must hold m.mu.
func (m *Manager) bindingOwnershipLocked(binding *ChannelBinding) bindingOwnership {
	if m.session == nil || binding.LastSessionID == "" || binding.LastSessionID == m.session.SessionID {
		return ownershipOwned
	}
	if m.foreignOwnerPossiblyAliveLocked() {
		return ownershipForeignLive
	}
	return ownershipForeignDead
}

// foreignOwnerPossiblyAliveLocked reports whether a foreign-owed binding's
// owner might still be running. Caller must hold m.mu (or accept the race).
// It consults the instance-detect registry: another LIVE instance in this
// workspace could own the binding → true. No other live instance (only self)
// → the owner must be dead → false. Unregistered detector (nil) → true
// (unknown, be conservative and keep the #689 rejection).
func (m *Manager) foreignOwnerPossiblyAliveLocked() bool {
	if m.instanceDetect == nil {
		return true
	}
	return len(m.instanceDetect.ListInstances()) > 1
}

// UnmuteAll unmutes all muted bindings for this process.
// Returns the number of adapters that were unmuted.
//
// #693: applies the same ownership rule as UnmuteBinding — bindings owned by
// another LIVE session are skipped (staying muted), while dead owners' orphan
// bindings are taken over and re-claimed for this session.
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

// DisableAll disables all ACTIVE (non-muted) bindings owned by this
// session. #1233: muted bindings — where #689/#693 park foreign-owned
// channels — are left untouched, and an unmuted binding that turns out to
// be foreign-live is skipped (same guard as UnmuteAll), so disable-all can
// no longer clear another live instance's claim and let a third instance
// hijack the channel.
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

// EnableAll re-enables all disabled bindings.
//
// #967: each enabled binding goes through the SAME claim path as
// EnableBinding, including the #689/#693 ownership guard — a binding owned by
// another LIVE session is skipped (stays disabled), everything else is
// re-claimed for this session both in memory and in the store.
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

// ReservePassiveSeqs atomically reserves n passive-reply seq slots for the
// binding matching (workspace, messageID) and returns the first usable seq
// (store count before reservation + 1). The reservation happens in the
// binding store under its cross-process file lock when the store implements
// PassiveSeqReserver (#3319: TUI + daemon instances each holding divergent
// in-memory counters previously emitted the same msg_seq and QQ deduplicated
// one send away). Stores without the capability fall back to the in-process
// counter (single-instance behavior, unchanged).
//
// Reserving before sending (rather than recording after) may skip seq
// numbers when a send later fails; skipping is safe - the server dedup key
// only requires uniqueness, not density.
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

// RecordPassiveReplyByMessage (#3326) locates the binding by its inbound
// message id alone (no workspace at hand on echo paths) and records one
// passive reply - everything under m.mu, unlike the adapter-side bare map
// range it replaces.
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

// recordPassiveReplyLocked advances one binding's passive-reply counter
// with the #967 persist-rollback discipline. Caller holds m.mu.
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
