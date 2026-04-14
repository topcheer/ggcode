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

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
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
	mu             sync.RWMutex
	bridge         Bridge
	session        *SessionBinding
	currentBinding *ChannelBinding
	bindingStore   BindingStore
	pairingStore   PairingStateStore
	pairingStates  map[string]PairingChannelState
	pendingPairing *PairingChallenge
	sinks          map[string]Sink
	adapters       map[string]AdapterState
	approvals      map[string]*pendingApproval
	onUpdate       func(StatusSnapshot)
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
		sinks:         make(map[string]Sink),
		adapters:      make(map[string]AdapterState),
		approvals:     make(map[string]*pendingApproval),
		pairingStates: make(map[string]PairingChannelState),
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
	m.currentBinding = nil
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
	if m.currentBinding == nil {
		return nil
	}
	copy := *m.currentBinding
	return &copy
}

func (m *Manager) ListBindings() ([]ChannelBinding, error) {
	m.mu.RLock()
	store := m.bindingStore
	maybeCurrent := m.currentBinding
	m.mu.RUnlock()
	if store == nil {
		if maybeCurrent == nil {
			return nil, nil
		}
		return []ChannelBinding{*maybeCurrent}, nil
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
	bridge := m.bridge
	sessionBound := m.session != nil
	binding := m.currentBinding
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
	if msg.Envelope.Adapter != binding.Adapter {
		m.mu.Unlock()
		return ErrInboundChannelDenied
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

	current := cloneBinding(m.currentBinding)
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

func (m *Manager) Emit(ctx context.Context, event OutboundEvent) error {
	m.mu.RLock()
	binding := m.currentBinding
	var sink Sink
	if binding != nil {
		sink = m.sinks[binding.Adapter]
	}
	m.mu.RUnlock()
	if binding == nil {
		return ErrNoChannelBound
	}
	if strings.TrimSpace(binding.ChannelID) == "" {
		return ErrNoChannelBound
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if sink == nil {
		return nil
	}
	return sink.Send(ctx, *binding, event)
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
	if m.currentBinding != nil {
		copy := *m.currentBinding
		snapshot.CurrentBinding = &copy
	}
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
	if m.bindingStore != nil {
		if err := m.bindingStore.Delete(workspace); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	if m.currentBinding != nil && m.currentBinding.Workspace == workspace {
		m.currentBinding = nil
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

func (m *Manager) ClearChannel(workspace string) error {
	m.mu.Lock()
	if workspace == "" && m.session != nil {
		workspace = m.session.Workspace
	}
	workspace = normalizeWorkspace(workspace)
	var binding *ChannelBinding
	if m.currentBinding != nil && m.currentBinding.Workspace == workspace {
		binding = m.currentBinding
	} else if m.bindingStore != nil {
		loaded, err := m.bindingStore.Load(workspace)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		binding = loaded
	}
	if binding == nil {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	binding.ChannelID = ""
	binding.ThreadID = ""
	binding.LastInboundMessageID = ""
	binding.LastInboundAt = time.Time{}
	binding.PassiveReplyCount = 0
	binding.PassiveReplyStartedAt = time.Time{}
	if m.bindingStore != nil {
		if err := m.bindingStore.Save(*binding); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	if m.currentBinding != nil && m.currentBinding.Workspace == workspace {
		m.currentBinding.ChannelID = ""
		m.currentBinding.ThreadID = ""
		m.currentBinding.LastInboundMessageID = ""
		m.currentBinding.LastInboundAt = time.Time{}
		m.currentBinding.PassiveReplyCount = 0
		m.currentBinding.PassiveReplyStartedAt = time.Time{}
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
	var binding *ChannelBinding
	if m.currentBinding != nil && m.currentBinding.Workspace == workspace {
		binding = m.currentBinding
	} else if m.bindingStore != nil {
		loaded, err := m.bindingStore.Load(workspace)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		binding = loaded
	}
	if binding == nil {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	binding.LastInboundMessageID = ""
	binding.LastInboundAt = time.Time{}
	binding.PassiveReplyCount = 0
	binding.PassiveReplyStartedAt = time.Time{}
	if m.bindingStore != nil {
		if err := m.bindingStore.Save(*binding); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	if m.currentBinding != nil && m.currentBinding.Workspace == workspace {
		m.currentBinding.LastInboundMessageID = ""
		m.currentBinding.LastInboundAt = time.Time{}
		m.currentBinding.PassiveReplyCount = 0
		m.currentBinding.PassiveReplyStartedAt = time.Time{}
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
	var binding *ChannelBinding
	if m.currentBinding != nil && m.currentBinding.Workspace == workspace {
		binding = m.currentBinding
	} else if m.bindingStore != nil {
		loaded, err := m.bindingStore.Load(workspace)
		if err != nil {
			m.mu.Unlock()
			return err
		}
		binding = loaded
	}
	if binding == nil {
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	if messageID == "" || strings.TrimSpace(binding.LastInboundMessageID) != messageID {
		m.mu.Unlock()
		return nil
	}
	if sentAt.IsZero() {
		sentAt = time.Now()
	}
	if binding.PassiveReplyStartedAt.IsZero() {
		binding.PassiveReplyStartedAt = sentAt
	}
	binding.PassiveReplyCount++
	if m.bindingStore != nil {
		if err := m.bindingStore.Save(*binding); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	if m.currentBinding != nil && m.currentBinding.Workspace == workspace {
		m.currentBinding.PassiveReplyCount = binding.PassiveReplyCount
		m.currentBinding.PassiveReplyStartedAt = binding.PassiveReplyStartedAt
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}
	return nil
}

func (m *Manager) reloadBindingLocked() error {
	m.currentBinding = nil
	if m.bindingStore == nil || m.session == nil {
		return nil
	}
	binding, err := m.bindingStore.Load(m.session.Workspace)
	if err != nil {
		return err
	}
	if binding != nil {
		copy := *binding
		m.currentBinding = &copy
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
		all, err := m.bindingStore.List()
		if err != nil {
			return ChannelBinding{}, err
		}
		for _, existing := range all {
			if existing.Adapter == binding.Adapter && existing.Workspace != binding.Workspace {
				return ChannelBinding{}, ErrAdapterAlreadyBound
			}
		}
		if err := m.bindingStore.Save(binding); err != nil {
			return ChannelBinding{}, err
		}
	}
	copy := binding
	m.currentBinding = &copy
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
