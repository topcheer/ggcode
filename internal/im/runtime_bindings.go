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
				// #520: a swallowed Save error means the token silently fails to
				// persist and WeChat stops responding after ~2 messages on
				// restart, with no log clue. Log it.
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
	// #967: cascade stopAdapter for any in-memory binding of this workspace,
	// same as UnbindSession (#719) - deleting the persisted binding alone left
	// the adapter goroutine/connection alive, delivering messages that hit
	// ErrNoChannelBound until instance exit.
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
	// Clear matching entries from currentBindings
	for name, b := range m.currentBindings {
		if normalizeWorkspace(b.Workspace) == workspace {
			delete(m.currentBindings, name)
		}
	}
	// #434: also purge DISABLED bindings for this workspace — a deleted
	// binding must not resurrect from disabledBindings via EnableAll/
	// EnableBinding after the user removed it.
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
	// #967: cascade stopAdapter $1 - a deleted
	// binding must not leave a live adapter delivering ErrNoChannelBound noise.
	m.stopAdapter(adapter)
	delete(m.currentBindings, adapter)
	// #434: purge any DISABLED entry for this adapter too, so a deleted
	// binding cannot resurrect via EnableAll/EnableBinding (ghost binding).
	delete(m.disabledBindings, adapter)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	// #434: every other binding mutation calls this; DeleteBinding was the
	// lone omission, leaving other instances' active-channel snapshots stale.
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

	// Atomically remove all existing bindings for this adapter (any workspace)
	// and save the new one. This prevents cross-process TOCTOU where two
	// instances in different workspaces could each read the file, delete the
	// other's binding, and write back — leaving the adapter double-bound.
	binding := ChannelBinding{
		Adapter:   adapterName,
		Workspace: workspace,
		BoundAt:   time.Now(),
	}

	if err := m.bindingStore.BindExclusive(binding); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("exclusive bind: %w", err)
	}

	// #434/#689: purge any DISABLED entry for this adapter. BindExclusive just
	// replaced every persisted binding for this adapter (any workspace), so any
	// disabledBindings tombstone is now a stale copy of a binding that no longer
	// exists — without this purge reloadBindingLocked skips the fresh binding
	// (dead adapter until restart) and EnableBinding would resurrect the stale
	// pre-rebind copy (old workspace) back into currentBindings. UnbindAdapter
	// and DeleteBinding do the same purge; this was the lone omission.
	delete(m.disabledBindings, adapterName)

	// Reload bindings to reflect the change
	if err := m.reloadBindingLocked(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("reload bindings: %w", err)
	}

	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()

	// Call the callback outside the lock
	if cb != nil {
		cb(snapshot)
	}
	// #689: every other binding mutation calls this; a rebind that moves the
	// adapter between workspaces must refresh other instances' snapshots too.
	m.syncInstanceActiveChannels()

	return nil
}

// UnbindAdapter removes the binding for whatever workspace has the given
// adapter name. This is needed when unbinding from a panel where the current
// session workspace may differ from the workspace that originally bound the
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
		// Idempotent delete: no persisted bindings for this adapter is a
		// successful no-op, not an error — the RemoveIMAdapter cascade
		// (#396) must not fail when a binding was already cleaned or never
		// existed.
		for _, b := range bindings {
			if err := m.bindingStore.Delete(b.Workspace, b.Adapter); err != nil {
				m.mu.Unlock()
				return err
			}
		}
	}
	// #967: cascade stopAdapter (#719 parity with UnbindSession) even when no
	// persisted binding existed (idempotent no-op path).
	m.stopAdapter(adapterName)
	delete(m.currentBindings, adapterName)
	// #434: purge any DISABLED entry for this adapter too (ghost-binding
	// prevention, same as DeleteBinding).
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
	// Persist: clear LastSessionID so other sessions can claim this adapter.
	// Disabled adapters are excluded from the startup claim logic.
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
	// #967 ownership guard, same rule as UnmuteBinding/UnmuteAll (#689/#693):
	// enabling a binding rewrites LastSessionID (the claim below), which must
	// not silently hijack a channel owned by another LIVE session. A DEAD
	// foreign owner (crash orphan) is still recoverable via takeover.
	switch m.bindingOwnershipLocked(binding) {
	case ownershipForeignLive:
		m.mu.Unlock()
		return fmt.Errorf("adapter %q is owned by another live session (last=%s); enable denied (disable elsewhere first or rebind to take over)", adapterName, binding.LastSessionID)
	case ownershipForeignDead:
		debug.Log("im", "EnableBinding: dead-owner takeover of %s from dead session=%s", adapterName, binding.LastSessionID)
	}
	copy := *binding
	// Claim this binding for our session.
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
	// Persist: claim this binding for our session.
	if store != nil && workspace != "" {
		if err := store.UpdateSessionID(workspace, adapterName, sessionID); err != nil {
			// #967: a swallowed claim error reports success while memory and
			// store diverge (another instance can steal the channel on its next
			// reload, causing enable->stolen->mute flapping). Propagate like the
			// #719 restart failure above.
			debug.Log("im", "EnableBinding: failed to set LastSessionID for %s: %v", adapterName, err)
			m.syncInstanceActiveChannels()
			return fmt.Errorf("enable adapter %s: ownership claim failed: %w", adapterName, err)
		}
	}
	if onRestart != nil {
		if err := onRestart(adapterName); err != nil {
			debug.Log("im", "enable restart adapter %s: %v", adapterName, err)
			// #719: a failed restart used to be swallowed into a debug log
			// while the API still reported success. Propagate so the caller
			// knows the adapter is enabled in state but not running.
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
	// Persist: keep LastSessionID on mute — mute is a temporary stop, not a
	// release of session ownership. Only disable/unbind releases ownership.
	// This prevents multi-instance races where one instance's mute clears
	// the session ownership for all other instances.
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
	// #689 ownership check: foreign-mute semantics (reloadBindingLocked) keep
	// other sessions' bindings in currentBindings with Muted=true, so "is muted"
	// alone does not mean we own this channel. Unconditionally calling
	// UpdateSessionID below let instance B steal session A's active channel:
	// B claims ownership + starts the adapter, A's next reload sees foreign and
	// mutes/stops — silent channel hijack. Reject when the persisted ownership
	// points at another session. (MuteBinding deliberately keeps LastSessionID
	// — "not a release of session ownership" — and UnmuteAll never rewrites
	// it, so claiming here was never the intended design.)
	//
	// #693 follow-up: distinguish a LIVE foreign owner (reject — the #689
	// hijack case) from a DEAD one (a crash, kill -9 or power loss never runs
	// the exit cleanup that clears LastSessionID, so the binding is an orphan
	// and would otherwise be stuck muted forever with no recovery path short
	// of disable+enable/rebind). Liveness is decided via the instance-detect
	// registry: if no other ggcode instance is alive in this workspace, the
	// foreign owner cannot be running — allow takeover (the UpdateSessionID
	// below re-claims it for this session). If other instances are alive we
	// cannot tell which one owns the binding, so stay conservative and reject.
	// instanceDetect == nil (never registered) is also conservative: reject.
	switch m.bindingOwnershipLocked(binding) {
	case ownershipForeignLive:
		m.mu.Unlock()
		return fmt.Errorf("adapter %q is owned by another live session (last=%s); unmute denied (disable+enable the adapter or rebind to take over)", adapterName, binding.LastSessionID)
	case ownershipForeignDead:
		debug.Log("im", "UnmuteBinding: dead-owner takeover of %s from dead session=%s", adapterName, binding.LastSessionID)
		// Keep in-memory ownership in sync with the persisted claim below.
		// (ownershipForeignDead implies m.session != nil.)
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
	// Persist: claim this binding for our session.
	if store != nil && workspace != "" {
		if err := store.UpdateSessionID(workspace, adapterName, sessionID); err != nil {
			// #967: same rationale as EnableBinding - a swallowed claim error
			// leaves memory claiming ownership the store does not reflect.
			debug.Log("im", "UnmuteBinding: failed to set LastSessionID for %s: %v", adapterName, err)
			m.syncInstanceActiveChannels()
			return fmt.Errorf("unmute adapter %s: ownership claim failed: %w", adapterName, err)
		}
	}
	if onRestart != nil {
		if err := onRestart(adapterName); err != nil {
			debug.Log("im", "unmute restart adapter %s: %v", adapterName, err)
			// #719: propagate restart failure instead of reporting success.
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
			// Same #689/#693 rule as UnmuteBinding: UnmuteAll must not become a
			// back door that grabs a live foreign owner's channel for the ~3s
			// window before the binding watcher re-mutes it.
			debug.Log("im", "UnmuteAll: skipping %s owned by live session=%s", name, binding.LastSessionID)
			continue
		case ownershipForeignDead:
			binding.LastSessionID = m.session.SessionID // implies m.session != nil
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
				// #719: propagate restart failures instead of reporting success.
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
	// #967: clear LastSessionID for every disabled binding, mirroring
	// DisableBinding ("so other sessions can claim this adapter"). Without
	// this the channel stays locked to this instance until it exits.
	if store != nil && workspace != "" {
		for _, name := range names {
			if err := store.UpdateSessionID(workspace, name, ""); err != nil {
				debug.Log("im", "DisableAll: failed to clear LastSessionID for %s: %v", name, err)
			}
		}
	}
	debug.Log("im", "DisableAll: disabled %d adapters: %v", count, names)
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
			// Same #689/#693 rule as UnmuteAll: EnableAll must not become a
			// back door that grabs a live foreign owner's channel.
			debug.Log("im", "EnableAll: skipping %s owned by live session=%s", name, binding.LastSessionID)
			skipped = append(skipped, name)
			continue
		case ownershipForeignDead:
			binding.LastSessionID = m.session.SessionID // implies m.session != nil
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
	// #967: persist the claim for every enabled binding (EnableBinding parity).
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
				// #719: propagate restart failures instead of reporting success.
				restartErrs = append(restartErrs, fmt.Errorf("adapter %s: %w", name, err))
			}
		}
	}
	_ = skipped
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
		// #967: snapshot before mutating so a persist failure can roll back
		// the counter (WeChat passive quota window would otherwise drift
		// ahead of what the store reflects).
		oldCount, oldStart := b.PassiveReplyCount, b.PassiveReplyStartedAt
		b.PassiveReplyCount++
		if m.bindingStore != nil {
			if err := m.persistBinding(*b); err != nil {
				b.PassiveReplyCount = oldCount
				b.PassiveReplyStartedAt = oldStart
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
