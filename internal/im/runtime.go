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
	mu                   sync.RWMutex
	bridge               Bridge
	session              *SessionBinding
	currentBindings      map[string]*ChannelBinding
	bindingStore         BindingStore
	pairingStore         PairingStateStore
	pairingStates        map[string]PairingChannelState
	pendingPairing       *PairingChallenge
	sinks                map[string]Sink
	adapters             map[string]AdapterState
	approvals            map[string]*pendingApproval
	approvalPrunerActive bool
	onUpdate             func(StatusSnapshot)

	seenMessages     map[string]time.Time
	seenMessageCount int

	disabledBindings map[string]*ChannelBinding

	adapterCancels map[string]context.CancelFunc

	onRestart func(adapterName string) error

	instanceDetect *InstanceDetect

	onInteractiveCallback func(InteractiveCallback)

	language string

	bindingWatcherCtx    context.Context
	bindingWatcherCancel context.CancelFunc
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

func (m *Manager) SetLanguage(lang string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.language = lang
	m.mu.Unlock()
}

func (m *Manager) Language() string {
	if m == nil {
		return "en"
	}
	m.mu.RLock()
	lang := m.language
	m.mu.RUnlock()
	if lang == "" {
		return "en"
	}
	return lang
}

func UnauthorizedMessage(lang string) string {
	if lang == "zh-CN" {
		return "你是未授权用户"
	}
	return "You are not an authorized user."
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
		adapterCancels:   make(map[string]context.CancelFunc),
	}
}

func (m *Manager) SetOnRestart(fn func(adapterName string) error) {
	debug.Log("im", "SetOnRestart: callback registered")
	m.mu.Lock()
	m.onRestart = fn
	m.mu.Unlock()
}

func (m *Manager) SetInteractiveCallback(fn func(InteractiveCallback)) {
	m.mu.Lock()
	m.onInteractiveCallback = fn
	m.mu.Unlock()
}

func (m *Manager) HandleInteractiveCallback(cb InteractiveCallback) {
	m.mu.RLock()
	fn := m.onInteractiveCallback
	m.mu.RUnlock()
	if fn != nil {
		fn(cb)
	}
}

func (m *Manager) RegisterInstance(workspace string, sessionID string) (*InstanceDetect, []InstanceInfo, error) {
	detect := NewInstanceDetect(workspace)
	others, err := detect.Register()
	if err != nil {
		return nil, nil, fmt.Errorf("register instance: %w", err)
	}

	m.mu.Lock()
	m.instanceDetect = detect
	m.mu.Unlock()

	m.claimUnclaimedBindings(sessionID)

	m.syncInstanceActiveChannels()

	if len(others) > 0 {

		count := m.muteNonOwnedBindings(sessionID)
		if count > 0 {
			debug.Log("im", "auto-muted %d channel(s) not owned by session=%s, primary PID=%d started=%s",
				count, sessionID, others[0].PID, others[0].StartedAt.Format("15:04:05"))
		}
	}

	return detect, others, nil
}

func (m *Manager) claimUnclaimedBindings(sessionID string) {
	if sessionID == "" {
		return
	}

	m.mu.Lock()
	store := m.bindingStore
	var workspace string
	if m.session != nil {
		workspace = m.session.Workspace
	}
	m.mu.Unlock()
	if store == nil || workspace == "" {
		return
	}
	bindings, err := store.ListByWorkspace(workspace)
	if err != nil {
		debug.Log("im", "claimUnclaimedBindings: ListByWorkspace error: %v", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session == nil {
		return
	}
	for _, b := range bindings {
		if b.LastSessionID != "" {
			continue
		}

		if _, disabled := m.disabledBindings[b.Adapter]; disabled {
			continue
		}

		if err := store.UpdateSessionID(workspace, b.Adapter, sessionID); err != nil {
			debug.Log("im", "claimUnclaimedBindings: UpdateSessionID error for %s: %v", b.Adapter, err)
		} else {
			debug.Log("im", "claimed binding %s for session=%s (was unclaimed)", b.Adapter, sessionID)
		}

		if cb, ok := m.currentBindings[b.Adapter]; ok {
			cb.LastSessionID = sessionID
		}
	}
}

func (m *Manager) StartUnstartedOwnedAdapters() {
	m.mu.Lock()
	var toStart []string
	sessionID := ""
	if m.session != nil {
		sessionID = m.session.SessionID
	}
	for name, binding := range m.currentBindings {
		if binding.Muted || strings.TrimSpace(name) == "" {
			continue
		}

		if binding.LastSessionID != "" && sessionID != "" && binding.LastSessionID != sessionID {
			continue
		}
		if _, hasSink := m.adapterCancels[name]; hasSink {
			continue
		}
		toStart = append(toStart, name)
	}
	onRestart := m.onRestart
	m.mu.Unlock()

	for _, name := range toStart {
		if onRestart != nil {
			if err := onRestart(name); err != nil {
				debug.Log("im", "StartUnstartedOwnedAdapters: failed to start %s: %v", name, err)
			}
		}
	}
}

func (m *Manager) muteNonOwnedBindings(sessionID string) int {
	m.mu.Lock()
	count := 0
	for name, binding := range m.currentBindings {

		if binding.LastSessionID != "" {
			continue
		}
		if binding.Muted {
			continue
		}
		binding.Muted = true
		m.stopAdapter(name)
		count++
	}
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if count > 0 {
		if cb != nil {
			cb(snapshot)
		}
		m.syncInstanceActiveChannels()
	}
	return count
}

func (m *Manager) UnregisterInstance() {
	m.mu.Lock()
	d := m.instanceDetect
	m.instanceDetect = nil
	m.mu.Unlock()
	if d != nil {
		d.Unregister()
	}
}

func (m *Manager) InstanceDetect() *InstanceDetect {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instanceDetect
}

func (m *Manager) IsPrimary() bool {
	m.mu.RLock()
	d := m.instanceDetect
	m.mu.RUnlock()
	if d == nil {
		return true
	}
	return d.IsPrimary()
}

func (m *Manager) syncInstanceActiveChannels() {
	m.mu.Lock()
	d := m.instanceDetect
	hasActive := false
	for _, b := range m.currentBindings {
		if !b.Muted && strings.TrimSpace(b.ChannelID) != "" {
			hasActive = true
			break
		}
	}
	m.mu.Unlock()
	if d != nil {
		_ = d.UpdateHasActiveChannels(hasActive)
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

func (m *Manager) AllPersistedBindings() []ChannelBinding {
	if m.bindingStore == nil {
		return nil
	}
	all, err := m.bindingStore.List()
	if err != nil {
		debug.Log("im", "AllPersistedBindings: List failed: %v", err)
		return nil
	}
	return all
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

	if old, ok := m.bridge.(interface{ detach() }); ok {
		old.detach()
	}
	m.bridge = bridge
}

func (m *Manager) SetOnUpdate(cb func(StatusSnapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUpdate = cb
}

func (m *Manager) IsMuted(adapterName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.currentBindings[adapterName]
	return ok && b.Muted
}

func (m *Manager) BindSession(binding SessionBinding) {
	m.mu.Lock()
	if binding.BoundAt.IsZero() {
		binding.BoundAt = time.Now()
	}

	var migrateOldSessionID, migrateWorkspace string
	if m.session != nil && m.session.Workspace == binding.Workspace && m.session.SessionID != binding.SessionID {
		migrateOldSessionID = m.session.SessionID
		migrateWorkspace = m.session.Workspace
	}

	copy := binding
	m.session = &copy

	if migrateOldSessionID != "" && m.bindingStore != nil {
		m.mu.Unlock()
		persisted, _ := m.bindingStore.ListByWorkspace(migrateWorkspace)
		m.mu.Lock()
		for _, b := range persisted {
			if b.LastSessionID == migrateOldSessionID {
				if err := m.bindingStore.UpdateSessionID(migrateWorkspace, b.Adapter, binding.SessionID); err != nil {
					debug.Log("im", "BindSession: migrate ownership %s: %s→%s error: %v", b.Adapter, migrateOldSessionID, binding.SessionID, err)
				} else {
					debug.Log("im", "BindSession: migrated ownership %s: %s→%s", b.Adapter, migrateOldSessionID, binding.SessionID)
				}
			}
		}
	}

	if err := m.reloadBindingLocked(); err != nil {
		debug.Log("im", "BindSession: reloadBindingLocked failed: %v", err)
	}

	prevCancel := m.bindingWatcherCancel
	m.bindingWatcherCancel = nil
	m.bindingWatcherCtx = nil
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if prevCancel != nil {
		prevCancel()
	}
	if cb != nil {
		cb(snapshot)
	}

	m.StartBindingWatcher()
}

func (m *Manager) UnbindSession() {
	m.mu.Lock()
	m.session = nil
	m.currentBindings = make(map[string]*ChannelBinding)
	m.disabledBindings = make(map[string]*ChannelBinding)

	for name := range m.adapterCancels {
		m.stopAdapter(name)
	}
	m.adapterCancels = make(map[string]context.CancelFunc)
	m.pendingPairing = nil

	watcherCancel := m.bindingWatcherCancel
	m.bindingWatcherCancel = nil
	m.bindingWatcherCtx = nil
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if watcherCancel != nil {
		watcherCancel()
	}
	if cb != nil {
		cb(snapshot)
	}
}

func (m *Manager) ClearSessionBindings(sessionID string) {
	if sessionID == "" {
		return
	}
	store := m.bindingStore
	if store == nil {
		return
	}
	clearSessionBindingsFromStore(store, sessionID)
}

func ClearSessionBindingsGlobal(sessionID string) {
	if sessionID == "" {
		return
	}
	path, err := DefaultBindingsPath()
	if err != nil {
		debug.Log("im", "ClearSessionBindingsGlobal: DefaultBindingsPath failed: %v", err)
		return
	}
	store, err := NewJSONFileBindingStore(path)
	if err != nil {
		debug.Log("im", "ClearSessionBindingsGlobal: could not open store: %v", err)
		return
	}
	clearSessionBindingsFromStore(store, sessionID)
}

func clearSessionBindingsFromStore(store BindingStore, sessionID string) {
	all, err := store.List()
	if err != nil {
		debug.Log("im", "clearSessionBindingsFromStore: List failed: %v", err)
		return
	}
	for _, b := range all {
		if b.LastSessionID == sessionID {
			if err := store.UpdateSessionID(b.Workspace, b.Adapter, ""); err != nil {
				debug.Log("im", "clearSessionBindingsFromStore: UpdateSessionID(%s) failed: %v", b.Adapter, err)
			} else {
				debug.Log("im", "clearSessionBindingsFromStore: cleared LastSessionID for adapter %s (was %s)", b.Adapter, sessionID)
			}
		}
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

func (m *Manager) CurrentSessionID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.session == nil {
		return ""
	}
	return m.session.SessionID
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

	sort.Slice(out, func(i, j int) bool {
		return out[i].Adapter < out[j].Adapter
	})
	return out
}

func (m *Manager) HasActiveBindings() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.currentBindings) > 0
}

func (m *Manager) HasNonInteractiveBindings(sentAdapters map[string]string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.currentBindings {
		if strings.TrimSpace(b.ChannelID) == "" {
			continue
		}
		if _, sent := sentAdapters[b.Adapter]; sent {
			continue
		}

		return true
	}
	return false
}

func (m *Manager) ListBindings() ([]ChannelBinding, error) {
	m.mu.RLock()
	store := m.bindingStore
	currentSnapshot := make([]ChannelBinding, 0, len(m.currentBindings))
	for _, b := range m.currentBindings {
		currentSnapshot = append(currentSnapshot, *b)
	}
	m.mu.RUnlock()

	sort.Slice(currentSnapshot, func(i, j int) bool {
		return currentSnapshot[i].Adapter < currentSnapshot[j].Adapter
	})
	if store == nil {
		if len(currentSnapshot) == 0 {
			return nil, nil
		}
		return currentSnapshot, nil
	}
	all, err := store.List()
	if err != nil {
		return currentSnapshot, nil
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].Adapter < all[j].Adapter
	})
	return all, nil
}

func (m *Manager) HandleInbound(ctx context.Context, msg InboundMessage) error {
	m.mu.Lock()

	dedupKey := ""
	if msgID := strings.TrimSpace(msg.Envelope.MessageID); msgID != "" {
		dedupKey = msg.Envelope.Adapter + ":" + msgID
		if _, seen := m.seenMessages[dedupKey]; seen {
			m.mu.Unlock()
			return nil
		}
		m.seenMessages[dedupKey] = time.Now()

		m.seenMessageCount++
		if m.seenMessageCount%100 == 0 {
			now := time.Now()
			for k, t := range m.seenMessages {
				if now.Sub(t) > 5*time.Minute {
					delete(m.seenMessages, k)
				}
			}
		}
	}

	rollbackDedup := func() {
		if dedupKey != "" {
			delete(m.seenMessages, dedupKey)
		}
	}

	bridge := m.bridge
	sessionBound := m.session != nil

	if b, ok := m.currentBindings[msg.Envelope.Adapter]; ok && b.Muted {
		m.mu.Unlock()
		return nil
	}

	binding := m.currentBindings[msg.Envelope.Adapter]
	changed := false
	if !sessionBound {
		rollbackDedup()
		m.mu.Unlock()
		return ErrNoSessionBound
	}
	if binding == nil {
		rollbackDedup()
		m.mu.Unlock()
		return ErrNoChannelBound
	}
	if bridge == nil {
		rollbackDedup()
		m.mu.Unlock()
		return ErrNoBridge
	}
	if msg.Envelope.ReceivedAt.IsZero() {
		msg.Envelope.ReceivedAt = time.Now()
	}

	orig := *binding
	needPersist := false
	if binding.ChannelID == "" && strings.TrimSpace(msg.Envelope.ChannelID) != "" {
		newChannelID := strings.TrimSpace(msg.Envelope.ChannelID)
		binding.ChannelID = newChannelID
		changed = true
		needPersist = true
	}

	if binding.ChannelID != "" && strings.TrimSpace(msg.Envelope.ChannelID) != binding.ChannelID {
		rollbackDedup()
		m.mu.Unlock()
		return ErrInboundChannelDenied
	}
	if inboundID := strings.TrimSpace(msg.Envelope.MessageID); inboundID != "" {
		binding.LastInboundMessageID = inboundID
		binding.LastInboundAt = msg.Envelope.ReceivedAt
		binding.PassiveReplyCount = 0
		binding.PassiveReplyStartedAt = time.Time{}
		changed = true
		needPersist = true
	}

	if needPersist && m.bindingStore != nil {
		if err := m.persistBinding(*binding); err != nil {

			binding.ChannelID = orig.ChannelID
			binding.LastInboundMessageID = orig.LastInboundMessageID
			binding.LastInboundAt = orig.LastInboundAt
			binding.PassiveReplyCount = orig.PassiveReplyCount
			binding.PassiveReplyStartedAt = orig.PassiveReplyStartedAt
			rollbackDedup()
			m.mu.Unlock()
			return err
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
	if err := bridge.SubmitInboundMessage(ctx, msg); err != nil {

		m.mu.Lock()
		rollbackDedup()
		m.mu.Unlock()
		return err
	}
	return nil
}

func (m *Manager) HandlePairingInbound(msg InboundMessage) (PairingResult, error) {
	m.mu.Lock()
	if m.session == nil {
		m.mu.Unlock()
		return PairingResult{}, ErrNoSessionBound
	}

	if b, ok := m.currentBindings[msg.Envelope.Adapter]; ok && b.Muted {
		m.mu.Unlock()
		return PairingResult{}, nil
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
			ReplyText: fmt.Sprintf("该 %s 渠道因多次被拒绝，已被加入黑名单。", msg.Envelope.Platform),
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
		sameChannel := pending.Adapter == msg.Envelope.Adapter && pending.ChannelID == channelID

		now := time.Now()
		age := now.Sub(pending.RequestedAt)
		lastActive := pending.RequestedAt
		if pending.LastInboundAt.After(lastActive) {
			lastActive = pending.LastInboundAt
		}
		idle := now.Sub(lastActive)
		if age > pairingChallengeTTL || (!sameChannel && idle > pairingPreemptIdleAfter) {
			debug.Log("im", "HandlePairingInbound: dropping stale pairing slot adapter=%s channel=%s age=%s idle=%s", pending.Adapter, pending.ChannelID, age.Truncate(time.Second), idle.Truncate(time.Second))
			m.pendingPairing = nil
		} else if sameChannel {
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
					reply = fmt.Sprintf("绑定成功，已切换到当前 %s 渠道。", msg.Envelope.Platform)
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

			if result, handled, err := m.recordWrongCodeLocked(key, pending, msg.Envelope.ReceivedAt); handled {
				m.mu.Unlock()
				return result, err
			}
			m.mu.Unlock()
			return PairingResult{
				Consumed:  true,
				Kind:      pending.Kind,
				ReplyText: "绑定码不正确，请输入屏幕上显示的 4 位绑定码。",
			}, nil
		} else {
			m.mu.Unlock()
			return PairingResult{
				Consumed:  true,
				Kind:      pending.Kind,
				ReplyText: fmt.Sprintf("当前已有其他渠道在等待绑定，请在对应 %s 渠道输入屏幕上的 4 位绑定码。", msg.Envelope.Platform),
			}, nil
		}
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

	if kind == PairingKindRebind && current != nil && strings.TrimSpace(current.ChannelID) != "" {
		notice := fmt.Sprintf("⚠ 有新渠道正在请求绑定到当前工作空间。配对码：%s\n如非本人操作请忽略，本人操作请在 ggcode 屏幕确认。", code)
		go func(b ChannelBinding) {
			defer safego.Recover("im.rebindNotice")
			_ = m.SendDirect(context.Background(), b, OutboundEvent{
				Kind: OutboundEventText,
				Text: notice,
			})
		}(*current)
	}
	return PairingResult{
		Consumed:  true,
		Kind:      kind,
		ReplyText: reply,
	}, nil
}

func (m *Manager) recordWrongCodeLocked(key string, pending PairingChallenge, receivedAt time.Time) (PairingResult, bool, error) {
	if m.pendingPairing == nil {
		return PairingResult{}, false, nil
	}
	m.pendingPairing.WrongCodeAttempts++
	m.pendingPairing.LastInboundAt = receivedAt
	if m.pendingPairing.WrongCodeAttempts < maxPairingWrongCodeAttempts {
		return PairingResult{}, false, nil
	}
	state := m.pairingStates[key]
	state.Adapter = pending.Adapter
	state.Platform = pending.Platform
	state.ChannelID = pending.ChannelID
	state.RejectCount++
	state.UpdatedAt = time.Now()
	if state.RejectCount >= pairingBlacklistAfterRejects && state.BlacklistedAt.IsZero() {
		state.BlacklistedAt = state.UpdatedAt
	}
	m.pairingStates[key] = state
	if err := m.savePairingStatesLocked(); err != nil {
		return PairingResult{}, true, err
	}
	m.pendingPairing = nil
	snapshot, cb := m.snapshotAndCallbackLocked()
	m.mu.Unlock()
	if cb != nil {
		cb(snapshot)
	}

	m.mu.Lock()
	return PairingResult{
		Consumed:  true,
		Kind:      pending.Kind,
		ReplyText: "绑定码错误次数过多，本次配对已取消。请重新发起配对请求获取新的绑定码。",
	}, true, nil
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
	if state.RejectCount >= pairingBlacklistAfterRejects {
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
	defaultSendTimeout = 30 * time.Second
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

			select {
			case <-ctx.Done():
				errsMu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", binding.Adapter, ctx.Err()))
				errsMu.Unlock()
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

func (m *Manager) EmitExcept(ctx context.Context, event OutboundEvent, excludeAdapter string) error {
	if excludeAdapter == "" {
		return m.Emit(ctx, event)
	}
	return m.EmitExceptAdapters(ctx, event, map[string]bool{excludeAdapter: true})
}

func (m *Manager) EmitExceptAdapters(ctx context.Context, event OutboundEvent, excludeSet map[string]bool) error {
	m.mu.RLock()
	var targets []emitTarget
	for _, b := range m.currentBindings {
		if strings.TrimSpace(b.ChannelID) == "" {
			continue
		}
		if excludeSet[b.Adapter] {
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

		return fmt.Errorf("IM adapter %q is not running", binding.Adapter)
	}

	return sendWithTimeout(ctx, sink, binding, event)
}

var ErrFileUploadUnsupported = errors.New("adapter does not support file upload")

func (m *Manager) SendFileDirect(ctx context.Context, binding ChannelBinding, file OutboundFile, caption string) error {
	m.mu.RLock()
	sink := m.sinks[binding.Adapter]
	m.mu.RUnlock()
	if strings.TrimSpace(binding.ChannelID) == "" {
		return ErrNoChannelBound
	}
	if sink == nil {
		return fmt.Errorf("IM adapter %q is not running", binding.Adapter)
	}
	fs, ok := sink.(FileSender)
	if !ok {
		return ErrFileUploadUnsupported
	}
	return fs.SendFile(ctx, binding, file, caption)
}

func (m *Manager) SendInteractive(ctx context.Context, msg InteractiveMessage) map[string]string {
	type interactiveTarget struct {
		binding ChannelBinding
		sender  InteractiveSender
	}

	m.mu.RLock()
	targets := make([]interactiveTarget, 0)
	for _, b := range m.currentBindings {
		if strings.TrimSpace(b.ChannelID) == "" {
			continue
		}
		sink := m.sinks[b.Adapter]
		if sink == nil {
			continue
		}
		is, ok := sink.(InteractiveSender)
		if !ok {
			continue
		}
		targets = append(targets, interactiveTarget{binding: *b, sender: is})
	}
	m.mu.RUnlock()

	result := make(map[string]string)
	for _, t := range targets {
		msgID, err := t.sender.SendInteractive(ctx, t.binding, msg)
		if err != nil {
			debug.Log("im", "interactive send failed adapter=%s: %v", t.binding.Adapter, err)
			continue
		}
		if msgID != "" {
			result[t.binding.Adapter] = msgID
		}
	}
	return result
}

func (m *Manager) EmitToNonInteractive(ctx context.Context, event OutboundEvent) error {
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

		if _, ok := sink.(InteractiveSender); ok {
			continue
		}
		targets = append(targets, emitTarget{binding: *b, sink: sink})
	}
	m.mu.RUnlock()
	if len(targets) == 0 {
		return nil
	}
	return fanOutSend(ctx, targets, event)
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

	m.pruneApprovalsLocked()

	m.startApprovalPrunerLocked()
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
	var mutedBindings []ChannelBinding
	for _, b := range m.currentBindings {
		if b.Muted {
			mutedBindings = append(mutedBindings, *b)
		}
	}
	snapshot.MutedBindings = mutedBindings
	sort.Slice(snapshot.MutedBindings, func(i, j int) bool {
		return snapshot.MutedBindings[i].Adapter < snapshot.MutedBindings[j].Adapter
	})
	snapshot.PendingApprovals = make([]ApprovalState, 0, len(m.approvals))
	for _, approval := range m.approvals {
		if !approval.state.Resolved {
			snapshot.PendingApprovals = append(snapshot.PendingApprovals, approval.state)
		}
	}
	sort.Slice(snapshot.PendingApprovals, func(i, j int) bool {
		return snapshot.PendingApprovals[i].Request.RequestedAt.Before(snapshot.PendingApprovals[j].Request.RequestedAt)
	})
	return snapshot
}

var approvalPruneInterval = time.Minute

func (m *Manager) pruneApprovalsLocked() {
	if len(m.approvals) == 0 {
		return
	}
	now := time.Now()
	for id, ap := range m.approvals {
		if ap.state.Resolved {
			delete(m.approvals, id)
		} else if now.Sub(ap.state.Request.RequestedAt) > time.Hour {

			ap.response <- permission.Deny
			close(ap.response)
			delete(m.approvals, id)
		}
	}
}

func (m *Manager) startApprovalPrunerLocked() {
	if m.approvalPrunerActive {
		return
	}
	m.approvalPrunerActive = true
	safego.Go("im.approval-pruner", func() {
		ticker := time.NewTicker(approvalPruneInterval)
		defer ticker.Stop()
		for range ticker.C {
			m.mu.Lock()
			m.pruneApprovalsLocked()
			empty := len(m.approvals) == 0
			if empty {
				m.approvalPrunerActive = false
			}
			snapshot, cb := m.snapshotAndCallbackLocked()
			m.mu.Unlock()
			if cb != nil {
				cb(snapshot)
			}
			if empty {
				return
			}
		}
	})
}

func (m *Manager) reloadBindingLocked() error {

	prevMuted := make(map[string]bool)
	for name, b := range m.currentBindings {
		if b.Muted {
			prevMuted[name] = true
		}
	}

	if m.bindingStore == nil || m.session == nil {
		return nil
	}
	m.currentBindings = make(map[string]*ChannelBinding)
	bindings, err := m.bindingStore.ListByWorkspace(m.session.Workspace)
	if err != nil {
		return err
	}

	sessionID := m.session.SessionID

	for i := range bindings {
		copy := bindings[i]
		copy.Muted = false

		isOwned := false
		isForeign := false
		if copy.LastSessionID != "" {
			if sessionID == "" || copy.LastSessionID != sessionID {
				isForeign = true
			} else {
				isOwned = true
			}
		}

		if isOwned {

			copy.Muted = false
		} else if isForeign {

			copy.Muted = true
		} else if prevMuted[copy.Adapter] {

			copy.Muted = true
		}

		if _, disabled := m.disabledBindings[copy.Adapter]; disabled {
			continue
		}

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

	if m.session != nil && binding.LastSessionID == "" {
		binding.LastSessionID = m.session.SessionID
	}
	if m.bindingStore != nil {

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
		if err := m.persistBinding(binding); err != nil {
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

func (m *Manager) ResolveOutputMode(adapterName, globalMode string) string {
	if m == nil {
		return globalMode
	}
	m.mu.RLock()
	state, ok := m.adapters[adapterName]
	m.mu.RUnlock()
	if !ok {
		return globalMode
	}
	switch state.Platform {
	case PlatformWechat:

		if globalMode == "" || globalMode == "verbose" {
			return WechatDefaultOutputMode
		}
	}
	return globalMode
}
