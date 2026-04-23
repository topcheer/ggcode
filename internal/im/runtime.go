package im

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

var (
	ErrNoSessionBound       = errors.New("no active session bound")
	ErrNoChannelBound       = errors.New("no channel bound for active workspace")
	ErrNoBridge             = errors.New("no IM bridge configured")
	ErrApprovalNotFound     = errors.New("approval not found")
	ErrAdapterAlreadyBound  = errors.New("adapter already bound to another workspace")
	ErrInboundChannelDenied = errors.New("inbound message does not match active binding")
	ErrNoPendingPairing     = errors.New("no pending pairing challenge")
)

type Manager struct {
	mu              sync.RWMutex
	bridge          Bridge
	session         *SessionBinding
	currentBindings map[string]*ChannelBinding // adapter name -> binding
	bindingStore    BindingStore
	pairingStore    PairingStateStore
	pairingStates   map[string]PairingChannelState
	pendingPairing  *PairingChallenge
	sinks           map[string]Sink
	adapters        map[string]AdapterState
	approvals       map[string]*pendingApproval
	onUpdate        func(StatusSnapshot)

	// Dedup inbound messages by adapter+messageID to prevent platforms
	// from delivering the same event twice (e.g. Feishu SDK retries).
	seenMessages map[string]time.Time

	// disabledBindings stores adapters that have been temporarily disabled.
	// The binding is moved out of currentBindings so Emit/HandleInbound skip it,
	// but the persistent binding is retained so EnableBinding can restore it.
	disabledBindings map[string]*ChannelBinding // adapter name -> binding

	// mutedBindings stores adapters muted for this process only.
	// Like disabledBindings, the binding is moved out of currentBindings
	// so the connection is dropped and inbound/outbound messages stop.
	// Unlike disabledBindings, this is purely in-memory and not persisted.
	// Cleared on restart — useful when multiple ggcode instances share the
	// same directory to avoid all receiving inbound messages simultaneously.
	mutedBindings map[string]*ChannelBinding // adapter name -> binding
}

type pendingApproval struct {
	state    ApprovalState
	response chan permission.Decision
}

type PairingResult struct {
	Consumed        bool
	Kind            PairingKind
	ReplyText       string
	Bound           bool
	PreviousBinding *ChannelBinding
	NewBinding      *ChannelBinding
}

func NewManager() *Manager {
	return &Manager{
		currentBindings:  make(map[string]*ChannelBinding),
		sinks:            make(map[string]Sink),
		adapters:         make(map[string]AdapterState),
		approvals:        make(map[string]*pendingApproval),
		pairingStates:    make(map[string]PairingChannelState),
		seenMessages:     make(map[string]time.Time),
		disabledBindings: make(map[string]*ChannelBinding),
		mutedBindings:    make(map[string]*ChannelBinding),
	}
}

func (m *Manager) SetBindingStore(store BindingStore) error {
	m.mu.Lock()
	m.bindingStore = store
	err := m.reloadBindingLocked()
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return err
}

func (m *Manager) SetPairingStore(store PairingStateStore) error {
	m.mu.Lock()
	m.pairingStore = store
	if store == nil {
		m.pairingStates = make(map[string]PairingChannelState)
		snapshot, cb := m.snapshotAndCallbackLocked()
		m.mu.Unlock()
		if cb != nil {
			cb(snapshot)
		}
		return nil
	}
	states, err := store.LoadAll()
	if err == nil {
		m.pairingStates = states
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return err
}

func (m *Manager) SetBridge(bridge Bridge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bridge = bridge
}

func (m *Manager) SetOnUpdate(cb func(StatusSnapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUpdate = cb
}

func (m *Manager) BindSession(binding SessionBinding) {
	m.mu.Lock()
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	copy := binding
	m.session = &copy
	_ = m.reloadBindingLocked()
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
}

func (m *Manager) UnbindSession() {
	m.mu.Lock()
	m.session = nil
	m.currentBindings = make(map[string]*ChannelBinding)
	m.disabledBindings = make(map[string]*ChannelBinding)
	m.mutedBindings = make(map[string]*ChannelBinding)
	m.pendingPairing = nil
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
}

func (m *Manager) ActiveSession() *SessionBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.session == nil {
		return nil
	}
	copy := *m.session
	return &copy
}

func (m *Manager) CurrentBinding() *ChannelBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.currentBindings {
		copy := *b
		return &copy
	}
	return nil
}

func (m *Manager) CurrentBindings() []ChannelBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChannelBinding, 0, len(m.currentBindings))
	for _, b := range m.currentBindings {
		out = append(out, *b)
	}
	return out
}

// HasActiveBindings returns true if there is at least one active channel binding.
// Lighter than ListBindings — no allocation or copying.
func (m *Manager) HasActiveBindings() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.currentBindings) > 0
}

func (m *Manager) ListBindings() ([]ChannelBinding, error) {
	m.mu.RLock()
	store := m.bindingStore
	currentSnapshot := make([]ChannelBinding, 0, len(m.currentBindings))
	for _, b := range m.currentBindings {
		currentSnapshot = append(currentSnapshot, *b)
	}
	m.mu.RUnlock()
	if store == nil {
		if len(currentSnapshot) == 0 {
			return nil, nil
		}
		return currentSnapshot, nil
	}
	return store.List()
}

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

func (m *Manager) HandleInbound(ctx context.Context, msg InboundMessage) error {
	m.mu.Lock()

	// Dedup: skip if we've already processed this message recently.
	if msgID := strings.TrimSpace(msg.Envelope.MessageID); msgID != "" {
		dedupKey := msg.Envelope.Adapter + ":" + msgID
		if _, seen := m.seenMessages[dedupKey]; seen {
			m.mu.Unlock()
			return nil
		}
		m.seenMessages[dedupKey] = time.Now()
		// Prune entries older than 5 minutes to bound memory.
		now := time.Now()
		for k, t := range m.seenMessages {
			if now.Sub(t) > 5*time.Minute {
				delete(m.seenMessages, k)
			}
		}
	}

	bridge := m.bridge
	sessionBound := m.session != nil

	// Check mute: silently drop inbound for muted adapters.
	if _, muted := m.mutedBindings[msg.Envelope.Adapter]; muted {
		m.mu.Unlock()
		return nil
	}

	binding := m.currentBindings[msg.Envelope.Adapter]
	changed := false
	if !sessionBound {
		m.mu.Unlock()
		return ErrNoSessionBound
	}
	if binding == nil {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	if bridge == nil {
		m.mu.Unlock()
		return ErrNoBridge
	}
	if msg.Envelope.ReceivedAt.IsZero() {
		msg.Envelope.ReceivedAt = time.Now()
	}
	if binding.ChannelID == "" && strings.TrimSpace(msg.Envelope.ChannelID) != "" {
		binding.ChannelID = strings.TrimSpace(msg.Envelope.ChannelID)
		changed = true
		if m.bindingStore != nil {
			if err := m.bindingStore.Save(*binding); err != nil {
				m.mu.Unlock()
				return err
			}
		}
	}
	if binding.ChannelID != "" && msg.Envelope.ChannelID != binding.ChannelID {
		m.mu.Unlock()
		return ErrInboundChannelDenied
	}
	if inboundID := strings.TrimSpace(msg.Envelope.MessageID); inboundID != "" {
		binding.LastInboundMessageID = inboundID
		binding.LastInboundAt = msg.Envelope.ReceivedAt
		binding.PassiveReplyCount = 0
		binding.PassiveReplyStartedAt = time.Time{}
		changed = true
		if m.bindingStore != nil {
			if err := m.bindingStore.Save(*binding); err != nil {
				m.mu.Unlock()
				return err
			}
		}
	}
	var snapshot StatusSnapshot
	var cb func(StatusSnapshot)
	if changed {
		snapshot, cb = m.snapshotAndCallbackLocked()
	}
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return bridge.SubmitInboundMessage(ctx, msg)
}

func (m *Manager) HandlePairingInbound(msg InboundMessage) (PairingResult, error) {
	m.mu.Lock()
	if m.session == nil {
		m.mu.Unlock()
		return PairingResult{}, ErrNoSessionBound
	}
	if msg.Envelope.ReceivedAt.IsZero() {
		msg.Envelope.ReceivedAt = time.Now()
	}
	channelID := strings.TrimSpace(msg.Envelope.ChannelID)
	if channelID == "" {
		m.mu.Unlock()
		return PairingResult{}, nil
	}
	key := pairingStateKey(msg.Envelope.Adapter, channelID)
	if state, ok := m.pairingStates[key]; ok && state.IsBlacklisted() {
		m.mu.Unlock()
		return PairingResult{
			Consumed:  true,
			ReplyText: "该 QQ 渠道已被拒绝绑定，不再接受新的配对请求。",
		}, nil
	}

	current := cloneBinding(m.currentBindings[msg.Envelope.Adapter])
	if current != nil &&
		current.Adapter == msg.Envelope.Adapter &&
		strings.TrimSpace(current.ChannelID) != "" &&
		current.ChannelID == channelID {
		m.mu.Unlock()
		return PairingResult{}, nil
	}

	if m.pendingPairing != nil {
		pending := *m.pendingPairing
		pending.ExistingBinding = cloneBinding(m.pendingPairing.ExistingBinding)
		if pending.Adapter == msg.Envelope.Adapter && pending.ChannelID == channelID {
			if normalizePairingCode(msg.Text) == pending.Code {
				newBinding := buildPairingBinding(msg, pending.ExistingBinding, m.session.Workspace)
				bound, err := m.bindChannelLocked(newBinding)
				if err != nil {
					m.mu.Unlock()
					return PairingResult{}, err
				}
				delete(m.pairingStates, key)
				if err := m.savePairingStatesLocked(); err != nil {
					m.mu.Unlock()
					return PairingResult{}, err
				}
				m.pendingPairing = nil
				snapshot, cb := m.snapshotAndCallbackLocked()
				m.mu.Unlock()
				if cb != nil {
					cb(snapshot)
				}
				var previous *ChannelBinding
				if pending.Kind == PairingKindRebind && pending.ExistingBinding != nil && strings.TrimSpace(pending.ExistingBinding.ChannelID) != "" {
					previous = cloneBinding(pending.ExistingBinding)
				}
				reply := "绑定成功，现在可以继续对话了。"
				if pending.Kind == PairingKindRebind {
					reply = "绑定成功，已切换到当前 QQ 渠道。"
				}
				copy := bound
				return PairingResult{
					Consumed:        true,
					Kind:            pending.Kind,
					ReplyText:       reply,
					Bound:           true,
					PreviousBinding: previous,
					NewBinding:      &copy,
				}, nil
			}
			m.mu.Unlock()
			return PairingResult{
				Consumed:  true,
				Kind:      pending.Kind,
				ReplyText: "绑定码不正确，请输入屏幕上显示的 4 位绑定码。",
			}, nil
		}
		m.mu.Unlock()
		return PairingResult{
			Consumed:  true,
			Kind:      pending.Kind,
			ReplyText: "当前已有其他渠道在等待绑定，请在对应 QQ 渠道输入屏幕上的 4 位绑定码。",
		}, nil
	}

	code, err := newPairingCode()
	if err != nil {
		m.mu.Unlock()
		return PairingResult{}, err
	}
	kind := PairingKindBind
	if current != nil && strings.TrimSpace(current.ChannelID) != "" {
		kind = PairingKindRebind
	}
	m.pendingPairing = &PairingChallenge{
		Kind:                 kind,
		Workspace:            m.session.Workspace,
		Adapter:              msg.Envelope.Adapter,
		Platform:             msg.Envelope.Platform,
		ChannelID:            channelID,
		ThreadID:             strings.TrimSpace(msg.Envelope.ThreadID),
		SenderID:             strings.TrimSpace(msg.Envelope.SenderID),
		SenderName:           strings.TrimSpace(msg.Envelope.SenderName),
		Code:                 code,
		RequestedAt:          msg.Envelope.ReceivedAt,
		LastInboundMessageID: strings.TrimSpace(msg.Envelope.MessageID),
		LastInboundAt:        msg.Envelope.ReceivedAt,
		ExistingBinding:      current,
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	reply := "请在 ggcode 屏幕上查看 4 位绑定码，并在这里输入完成绑定。"
	if kind == PairingKindRebind {
		reply = "该 bot 当前已绑定其他渠道。请在 ggcode 屏幕上查看 4 位绑定码，并在这里输入完成切换。"
	}
	return PairingResult{
		Consumed:  true,
		Kind:      kind,
		ReplyText: reply,
	}, nil
}

func (m *Manager) RejectPendingPairing() (*PairingChallenge, bool, error) {
	m.mu.Lock()
	if m.pendingPairing == nil {
		m.mu.Unlock()
		return nil, false, ErrNoPendingPairing
	}
	challenge := *m.pendingPairing
	challenge.ExistingBinding = cloneBinding(m.pendingPairing.ExistingBinding)
	key := pairingStateKey(challenge.Adapter, challenge.ChannelID)
	state := m.pairingStates[key]
	state.Adapter = challenge.Adapter
	state.Platform = challenge.Platform
	state.ChannelID = challenge.ChannelID
	state.RejectCount++
	state.UpdatedAt = time.Now()
	blacklisted := false
	if state.RejectCount >= 3 {
		if state.BlacklistedAt.IsZero() {
			state.BlacklistedAt = state.UpdatedAt
		}
		blacklisted = true
	}
	m.pairingStates[key] = state
	if err := m.savePairingStatesLocked(); err != nil {
		m.mu.Unlock()
		return nil, false, err
	}
	m.pendingPairing = nil
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return &challenge, blacklisted, nil
}

const (
	defaultSendTimeout = 10 * time.Second
	maxSendRetries     = 1
	retryDelay         = 500 * time.Millisecond
)

type emitTarget struct {
	binding ChannelBinding
	sink    Sink
}

func sendWithTimeout(ctx context.Context, sink Sink, binding ChannelBinding, event OutboundEvent) error {
	var lastErr error
	for attempt := 0; attempt <= maxSendRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
			}
		}
		sendCtx, cancel := context.WithTimeout(ctx, defaultSendTimeout)
		err := sink.Send(sendCtx, binding, event)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		debug.Log("im-send", "adapter=%s channel=%s attempt=%d error: %v", binding.Adapter, binding.ChannelID, attempt+1, err)
	}
	return lastErr
}

func fanOutSend(ctx context.Context, targets []emitTarget, event OutboundEvent) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)
	for _, t := range targets {
		wg.Add(1)
		binding := t.binding
		sink := t.sink
		safego.Go("im.runtime.fanOut", func() {
			defer wg.Done()
			// Check if context is already cancelled before sending
			select {
			case <-ctx.Done():
				return
			default:
			}
			if err := sendWithTimeout(ctx, sink, binding, event); err != nil {
				errsMu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", binding.Adapter, err))
				errsMu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("fanOutSend: %d adapter(s) failed: %w", len(errs), errors.Join(errs...))
	}
	return nil
}

func (m *Manager) Emit(ctx context.Context, event OutboundEvent) error {
	m.mu.RLock()
	var targets []emitTarget
	for _, b := range m.currentBindings {
		if strings.TrimSpace(b.ChannelID) == "" {
			continue
		}
		sink := m.sinks[b.Adapter]
		if sink == nil {
			continue
		}
		targets = append(targets, emitTarget{binding: *b, sink: sink})
	}
	m.mu.RUnlock()
	if len(targets) == 0 {
		return ErrNoChannelBound
	}
	return fanOutSend(ctx, targets, event)
}

// EmitExcept sends an event to all bound channels except those matching excludeAdapter.
// This is used to suppress user mirror echo on the originating IM channel while still
// delivering it to other bound channels.
func (m *Manager) EmitExcept(ctx context.Context, event OutboundEvent, excludeAdapter string) error {
	if excludeAdapter == "" {
		return m.Emit(ctx, event)
	}
	m.mu.RLock()
	var targets []emitTarget
	for _, b := range m.currentBindings {
		if strings.TrimSpace(b.ChannelID) == "" {
			continue
		}
		if b.Adapter == excludeAdapter {
			continue
		}
		sink := m.sinks[b.Adapter]
		if sink == nil {
			continue
		}
		targets = append(targets, emitTarget{binding: *b, sink: sink})
	}
	m.mu.RUnlock()
	if len(targets) == 0 {
		return ErrNoChannelBound
	}
	return fanOutSend(ctx, targets, event)
}

func (m *Manager) SendDirect(ctx context.Context, binding ChannelBinding, event OutboundEvent) error {
	m.mu.RLock()
	sink := m.sinks[binding.Adapter]
	m.mu.RUnlock()
	if strings.TrimSpace(binding.ChannelID) == "" {
		return ErrNoChannelBound
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if sink == nil {
		return nil
	}
	return sink.Send(ctx, binding, event)
}

func (m *Manager) RegisterApproval(req ApprovalRequest) (ApprovalRequest, <-chan permission.Decision) {
	m.mu.Lock()
	if req.ID == "" {
		req.ID = newID()
	}
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now()
	}
	resp := make(chan permission.Decision, 1)
	m.approvals[req.ID] = &pendingApproval{
		state:    ApprovalState{Request: req},
		response: resp,
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return req, resp
}

func (m *Manager) ResolveApproval(resp ApprovalResponse) (ApprovalResult, bool, error) {
	m.mu.Lock()
	pending, ok := m.approvals[resp.ApprovalID]
	if !ok {
		m.mu.Unlock()
		return ApprovalResult{}, false, ErrApprovalNotFound
	}
	if pending.state.Resolved {
		m.mu.Unlock()
		return approvalResultFromState(pending.state), false, nil
	}
	if resp.RespondedAt.IsZero() {
		resp.RespondedAt = time.Now()
	}
	pending.state.Resolved = true
	pending.state.Decision = resp.Decision
	pending.state.RespondedBy = resp.RespondedBy
	pending.state.RespondedAt = resp.RespondedAt
	pending.response <- resp.Decision
	close(pending.response)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return approvalResultFromState(pending.state), true, nil
}

func (m *Manager) Snapshot() StatusSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() StatusSnapshot {
	var snapshot StatusSnapshot
	if m.session != nil {
		copy := *m.session
		snapshot.ActiveSession = &copy
	}
	snapshot.CurrentBindings = make([]ChannelBinding, 0, len(m.currentBindings))
	for _, b := range m.currentBindings {
		snapshot.CurrentBindings = append(snapshot.CurrentBindings, *b)
	}
	sort.Slice(snapshot.CurrentBindings, func(i, j int) bool {
		return snapshot.CurrentBindings[i].Adapter < snapshot.CurrentBindings[j].Adapter
	})
	if m.pendingPairing != nil {
		copy := *m.pendingPairing
		copy.ExistingBinding = cloneBinding(m.pendingPairing.ExistingBinding)
		snapshot.PendingPairing = &copy
	}
	snapshot.Adapters = make([]AdapterState, 0, len(m.adapters))
	for _, adapter := range m.adapters {
		snapshot.Adapters = append(snapshot.Adapters, adapter)
	}
	sort.Slice(snapshot.Adapters, func(i, j int) bool {
		return snapshot.Adapters[i].Name < snapshot.Adapters[j].Name
	})
	snapshot.DisabledBindings = make([]ChannelBinding, 0, len(m.disabledBindings))
	for _, b := range m.disabledBindings {
		snapshot.DisabledBindings = append(snapshot.DisabledBindings, *b)
	}
	sort.Slice(snapshot.DisabledBindings, func(i, j int) bool {
		return snapshot.DisabledBindings[i].Adapter < snapshot.DisabledBindings[j].Adapter
	})
	snapshot.MutedBindings = make([]ChannelBinding, 0, len(m.mutedBindings))
	for _, b := range m.mutedBindings {
		snapshot.MutedBindings = append(snapshot.MutedBindings, *b)
	}
	sort.Slice(snapshot.MutedBindings, func(i, j int) bool {
		return snapshot.MutedBindings[i].Adapter < snapshot.MutedBindings[j].Adapter
	})
	snapshot.PendingApprovals = make([]ApprovalState, 0, len(m.approvals))
	for _, approval := range m.approvals {
		snapshot.PendingApprovals = append(snapshot.PendingApprovals, approval.state)
	}
	sort.Slice(snapshot.PendingApprovals, func(i, j int) bool {
		return snapshot.PendingApprovals[i].Request.RequestedAt.Before(snapshot.PendingApprovals[j].Request.RequestedAt)
	})
	return snapshot
}

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
	return bound, nil
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
			if err := m.bindingStore.Save(b); err != nil {
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
	copy := *binding
	m.disabledBindings[adapterName] = &copy
	delete(m.currentBindings, adapterName)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
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
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
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

// MuteBinding mutes an adapter for this process only. Like DisableBinding,
// the binding is moved out of currentBindings so the connection drops and
// inbound/outbound messages stop. Unlike DisableBinding, this is not persisted
// and is cleared on restart.
func (m *Manager) MuteBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.currentBindings[adapterName]
	if !ok {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	copy := *binding
	m.mutedBindings[adapterName] = &copy
	delete(m.currentBindings, adapterName)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

// UnmuteBinding unmutes a previously muted adapter for this process.
func (m *Manager) UnmuteBinding(adapterName string) error {
	m.mu.Lock()
	binding, ok := m.mutedBindings[adapterName]
	if !ok {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	copy := *binding
	m.currentBindings[adapterName] = &copy
	delete(m.mutedBindings, adapterName)
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

// MuteAll mutes all currently active bindings for this process.
// Returns the number of adapters that were muted.
func (m *Manager) MuteAll() (int, error) {
	m.mu.Lock()
	count := 0
	for name, binding := range m.currentBindings {
		copy := *binding
		m.mutedBindings[name] = &copy
		delete(m.currentBindings, name)
		count++
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return count, nil
}

// UnmuteAll unmutes all muted bindings for this process.
// Returns the number of adapters that were unmuted.
func (m *Manager) UnmuteAll() (int, error) {
	m.mu.Lock()
	count := 0
	for name, binding := range m.mutedBindings {
		copy := *binding
		m.currentBindings[name] = &copy
		delete(m.mutedBindings, name)
		count++
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return count, nil
}

// MutedBindings returns a snapshot of currently muted bindings.
func (m *Manager) MutedBindings() []ChannelBinding {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChannelBinding, 0, len(m.mutedBindings))
	for _, b := range m.mutedBindings {
		out = append(out, *b)
	}
	return out
}

// IsBindingMuted returns true if the given adapter's binding is currently muted.
func (m *Manager) IsBindingMuted(adapterName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.mutedBindings[adapterName]
	return ok
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
			if err := m.bindingStore.Save(bindings[i]); err != nil {
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
				if err := m.bindingStore.Save(*b); err != nil {
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

func (m *Manager) SyncSessionHistory(ctx context.Context, messages []provider.Message) error {
	for _, event := range SessionHistoryEvents(messages) {
		if err := m.Emit(ctx, event); err != nil {
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
			if err := m.bindingStore.Save(*b); err != nil {
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
		if err := m.bindingStore.Save(*b); err != nil {
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

func (m *Manager) reloadBindingLocked() error {
	m.currentBindings = make(map[string]*ChannelBinding)
	if m.bindingStore == nil || m.session == nil {
		return nil
	}
	bindings, err := m.bindingStore.ListByWorkspace(m.session.Workspace)
	if err != nil {
		return err
	}
	for i := range bindings {
		copy := bindings[i]
		m.currentBindings[copy.Adapter] = &copy
	}
	return nil
}

func (m *Manager) bindChannelLocked(binding ChannelBinding) (ChannelBinding, error) {
	if binding.Workspace == "" && m.session != nil {
		binding.Workspace = m.session.Workspace
	}
	binding.Workspace = normalizeWorkspace(binding.Workspace)
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}
	if m.bindingStore != nil {
		// Auto-unbind: if adapter is bound to another workspace, remove old binding
		existing, err := m.bindingStore.ListByAdapter(binding.Adapter)
		if err != nil {
			return ChannelBinding{}, err
		}
		for _, old := range existing {
			if normalizeWorkspace(old.Workspace) != binding.Workspace {
				if err := m.bindingStore.Delete(old.Workspace, old.Adapter); err != nil {
					return ChannelBinding{}, err
				}
			}
		}
		if err := m.bindingStore.Save(binding); err != nil {
			return ChannelBinding{}, err
		}
	}
	copy := binding
	m.currentBindings[copy.Adapter] = &copy
	return binding, nil
}

func (m *Manager) savePairingStatesLocked() error {
	if m.pairingStore == nil {
		return nil
	}
	states := make(map[string]PairingChannelState, len(m.pairingStates))
	for key, value := range m.pairingStates {
		states[key] = value
	}
	return m.pairingStore.SaveAll(states)
}

func (m *Manager) snapshotAndCallbackLocked() (StatusSnapshot, func(StatusSnapshot)) {
	return m.snapshotLocked(), m.onUpdate
}

func approvalResultFromState(state ApprovalState) ApprovalResult {
	return ApprovalResult{
		Request:     state.Request,
		Decision:    state.Decision,
		RespondedBy: state.RespondedBy,
		RespondedAt: state.RespondedAt,
	}
}

func cloneBinding(binding *ChannelBinding) *ChannelBinding {
	if binding == nil {
		return nil
	}
	copy := *binding
	return &copy
}

func buildPairingBinding(msg InboundMessage, existing *ChannelBinding, workspace string) ChannelBinding {
	binding := ChannelBinding{
		Workspace:            workspace,
		Platform:             msg.Envelope.Platform,
		Adapter:              msg.Envelope.Adapter,
		TargetID:             firstNonEmpty(strings.TrimSpace(msg.Envelope.SenderID), strings.TrimSpace(msg.Envelope.ChannelID)),
		ChannelID:            strings.TrimSpace(msg.Envelope.ChannelID),
		ThreadID:             strings.TrimSpace(msg.Envelope.ThreadID),
		LastInboundMessageID: strings.TrimSpace(msg.Envelope.MessageID),
		LastInboundAt:        msg.Envelope.ReceivedAt,
	}
	if existing != nil {
		if strings.TrimSpace(existing.Workspace) != "" {
			binding.Workspace = existing.Workspace
		}
		if existing.Adapter == msg.Envelope.Adapter && strings.TrimSpace(existing.TargetID) != "" {
			binding.TargetID = existing.TargetID
		}
	}
	return binding
}

func normalizePairingCode(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	var digits strings.Builder
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 4 {
		return digits.String()
	}
	return trimmed
}

func newPairingCode() (string, error) {
	var raw [2]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}
	return fmt.Sprintf("%04d", int(binary.BigEndian.Uint16(raw[:]))%10000), nil
}

func newID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(raw[:])
}

// PCAdapter returns the first registered PrivateClaw adapter, or nil.
func (m *Manager) PCAdapter() PCAdapterAPI {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sink := range m.sinks {
		if a, ok := sink.(*pcAdapter); ok {
			return a
		}
	}
	return nil
}
