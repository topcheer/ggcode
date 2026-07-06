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
	seenMessages     map[string]time.Time
	seenMessageCount int

	// disabledBindings stores adapters that have been temporarily disabled.
	// The binding is moved out of currentBindings so Emit/HandleInbound skip it,
	// but the persistent binding is retained so EnableBinding can restore it.
	disabledBindings map[string]*ChannelBinding // adapter name -> binding

	// adapterCancels stores per-adapter cancel functions so that
	// mute/disable can stop the adapter's goroutine and drop its connection.
	adapterCancels map[string]context.CancelFunc

	// onRestart is called by UnmuteBinding/EnableBinding to restart an
	// adapter after it was previously stopped. Set by the TUI/daemon layer
	// which has access to the IM config.
	onRestart func(adapterName string) error

	// instanceDetect tracks this process's registration in .ggcode/instances/
	// for multi-instance detection. Set via RegisterInstance.
	instanceDetect *InstanceDetect

	// onInteractiveCallback is called when an adapter receives an interactive
	// button/menu callback. Set via SetInteractiveCallback.
	onInteractiveCallback func(InteractiveCallback)

	// language stores the UI language ("zh-CN" or "en") so adapters can
	// localize messages. Set via SetLanguage, typically from NewIMEmitter.
	language string
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

// SetLanguage sets the UI language for adapter message localization.
// Typically called once during initialization from NewIMEmitter.
func (m *Manager) SetLanguage(lang string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.language = lang
	m.mu.Unlock()
}

// Language returns the configured UI language ("zh-CN" or "en").
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

// UnauthorizedMessage returns the localized "unauthorized user" message
// for use by IM adapters when HandleInbound returns ErrInboundChannelDenied.
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

// SetOnRestart sets a callback invoked by UnmuteBinding/EnableBinding to
// restart a previously stopped adapter. The caller (TUI/daemon) provides
// this because it has access to the IM config needed to start adapters.
func (m *Manager) SetOnRestart(fn func(adapterName string) error) {
	debug.Log("im", "SetOnRestart: callback registered")
	m.mu.Lock()
	m.onRestart = fn
	m.mu.Unlock()
}

// SetInteractiveCallback registers a handler for interactive button/menu callbacks
// from adapters. When a user clicks a button, the adapter calls
// Manager.HandleInteractiveCallback which dispatches to this handler.
func (m *Manager) SetInteractiveCallback(fn func(InteractiveCallback)) {
	m.mu.Lock()
	m.onInteractiveCallback = fn
	m.mu.Unlock()
}

// HandleInteractiveCallback is called by adapters when a user clicks an
// interactive button. It dispatches to the registered callback handler.
func (m *Manager) HandleInteractiveCallback(cb InteractiveCallback) {
	m.mu.RLock()
	fn := m.onInteractiveCallback
	m.mu.RUnlock()
	if fn != nil {
		fn(cb)
	}
}

// RegisterInstance creates an InstanceDetect for the given workspace, registers
// this process as a running instance, and stores it on the Manager.
// If other instances are already running in the same workspace, this instance
// is auto-muted (non-primary). Returns the detector and any other live instances.
func (m *Manager) RegisterInstance(workspace string) (*InstanceDetect, []InstanceInfo, error) {
	detect := NewInstanceDetect(workspace)
	others, err := detect.Register()
	if err != nil {
		return nil, nil, fmt.Errorf("register instance: %w", err)
	}

	m.mu.Lock()
	m.instanceDetect = detect
	m.mu.Unlock()

	// Sync HasActiveChannels based on current bindings
	m.syncInstanceActiveChannels()

	if len(others) > 0 {
		count, _ := m.MuteAll()
		if count > 0 {
			debug.Log("im", "auto-muted %d channel(s), primary PID=%d started=%s",
				count, others[0].PID, others[0].StartedAt.Format("15:04:05"))
		}
	}

	return detect, others, nil
}

// UnregisterInstance removes this process's instance registration (PID file).
// Call on graceful shutdown.
func (m *Manager) UnregisterInstance() {
	m.mu.Lock()
	d := m.instanceDetect
	m.instanceDetect = nil
	m.mu.Unlock()
	if d != nil {
		d.Unregister()
	}
}

// InstanceDetect returns the instance detector (nil if not registered).
func (m *Manager) InstanceDetect() *InstanceDetect {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.instanceDetect
}

// IsPrimary returns true if this instance is the oldest running instance
// in the workspace (or no detector is set).
func (m *Manager) IsPrimary() bool {
	m.mu.RLock()
	d := m.instanceDetect
	m.mu.RUnlock()
	if d == nil {
		return true
	}
	return d.IsPrimary()
}

// syncInstanceActiveChannels updates the HasActiveChannels flag in the
// instance PID file based on current non-muted bindings with valid channel IDs.
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

// AllPersistedBindings returns all persisted bindings from the store,
// including bindings for other workspaces not currently active.
func (m *Manager) AllPersistedBindings() []ChannelBinding {
	if m.bindingStore == nil {
		return nil
	}
	all, err := m.bindingStore.List()
	if err != nil {
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
	m.bridge = bridge
}

func (m *Manager) SetOnUpdate(cb func(StatusSnapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onUpdate = cb
}

// IsMuted returns the runtime muted state for an adapter.
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
	m.adapterCancels = make(map[string]context.CancelFunc)
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

// HasNonInteractiveBindings returns true if there is at least one bound adapter
// that did NOT receive an interactive message (i.e. doesn't implement InteractiveSender
// or wasn't in the sentAdapters set). Used to decide whether to send text fallback.
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
		// This binding was not covered by interactive — needs fallback
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
	if store == nil {
		if len(currentSnapshot) == 0 {
			return nil, nil
		}
		return currentSnapshot, nil
	}
	return store.List()
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
		// Only prune every 100 messages to avoid O(n) on every inbound.
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

	bridge := m.bridge
	sessionBound := m.session != nil

	// Check mute: silently drop inbound for muted adapters.
	if b, ok := m.currentBindings[msg.Envelope.Adapter]; ok && b.Muted {
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
		newChannelID := strings.TrimSpace(msg.Envelope.ChannelID)
		if m.bindingStore != nil {
			probe := *binding
			probe.ChannelID = newChannelID
			if err := m.persistBinding(probe); err != nil {
				m.mu.Unlock()
				return err
			}
		}
		binding.ChannelID = newChannelID
		changed = true
	}
	if binding.ChannelID != "" && msg.Envelope.ChannelID != binding.ChannelID {
		m.mu.Unlock()
		return ErrInboundChannelDenied
	}
	if inboundID := strings.TrimSpace(msg.Envelope.MessageID); inboundID != "" {
		if m.bindingStore != nil {
			probe := *binding
			probe.LastInboundMessageID = inboundID
			probe.LastInboundAt = msg.Envelope.ReceivedAt
			probe.PassiveReplyCount = 0
			probe.PassiveReplyStartedAt = time.Time{}
			if err := m.persistBinding(probe); err != nil {
				m.mu.Unlock()
				return err
			}
		}
		binding.LastInboundMessageID = inboundID
		binding.LastInboundAt = msg.Envelope.ReceivedAt
		binding.PassiveReplyCount = 0
		binding.PassiveReplyStartedAt = time.Time{}
		changed = true
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

// HandlePairingInbound processes an inbound IM message for pairing.
// If the adapter has no binding with a ChannelID, it creates a pairing challenge
// (4-digit code displayed on screen). The user must enter the code in the IM channel
// to complete binding and obtain the ChannelID/TargetID.
func (m *Manager) HandlePairingInbound(msg InboundMessage) (PairingResult, error) {
	m.mu.Lock()
	if m.session == nil {
		m.mu.Unlock()
		return PairingResult{}, ErrNoSessionBound
	}
	// Silently ignore pairing for muted adapters
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
			ReplyText: fmt.Sprintf("当前已有其他渠道在等待绑定，请在对应 %s 渠道输入屏幕上的 4 位绑定码。", msg.Envelope.Platform),
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
	return m.EmitExceptAdapters(ctx, event, map[string]bool{excludeAdapter: true})
}

// EmitExceptAdapters sends an event to all bound channels except those in excludeSet.
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
		return nil
	}
	return sink.Send(ctx, binding, event)
}

// SendInteractive sends an interactive message to all bound adapters that
// support it. Returns a map of adapter name → platform message ID for
// callback correlation. Adapters that don't support interactive messages
// are skipped (caller should fall back to text).
func (m *Manager) SendInteractive(ctx context.Context, msg InteractiveMessage) map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]string)
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
		msgID, err := is.SendInteractive(ctx, *b, msg)
		if err != nil {
			debug.Log("im", "interactive send failed adapter=%s: %v", b.Adapter, err)
			continue
		}
		if msgID != "" {
			result[b.Adapter] = msgID
		}
	}
	return result
}

// EmitToNonInteractive sends an event only to adapters that do NOT implement
// InteractiveSender. Used for ask_user text fallback — adapters that already
// received interactive buttons should not get the duplicate text version.
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
		// Skip adapters that support interactive messages
		if _, ok := sink.(InteractiveSender); ok {
			continue
		}
		targets = append(targets, emitTarget{binding: *b, sink: sink})
	}
	m.mu.RUnlock()
	if len(targets) == 0 {
		return nil // no non-interactive adapters, that's fine
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
	// Prune stale entries to bound memory. Resolved entries older than
	// 5 minutes and unresolved entries older than 1 hour are removed.
	m.pruneApprovalsLocked()
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

// pruneApprovalsLocked removes stale entries from the approvals map to bound
// memory. Resolved entries are removed immediately (the snapshot was already
// taken at resolution time). Unresolved entries older than 1 hour are removed
// (the user likely abandoned them). Must be called with m.mu held.
func (m *Manager) pruneApprovalsLocked() {
	if len(m.approvals) < 32 {
		return // not worth pruning for small maps
	}
	now := time.Now()
	for id, ap := range m.approvals {
		if ap.state.Resolved {
			delete(m.approvals, id)
		} else if now.Sub(ap.state.Request.RequestedAt) > time.Hour {
			// Abandoned approval — close the channel and remove.
			close(ap.response)
			delete(m.approvals, id)
		}
	}
}

func (m *Manager) reloadBindingLocked() error {
	// Preserve runtime-only state (muted) from existing bindings before reload.
	// Without this, calling BindSession after RegisterInstance+MuteAll would
	// wipe the auto-mute state, causing all instances to show active IM channels.
	prevMuted := make(map[string]bool)
	for name, b := range m.currentBindings {
		if b.Muted {
			prevMuted[name] = true
		}
	}

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
		copy.Muted = false // Muted is in-memory only, never restored from store
		// Preserve muted state from before reload (e.g. auto-mute from RegisterInstance)
		if prevMuted[copy.Adapter] {
			copy.Muted = true
		}
		// Skip adapters that were explicitly disabled via ApplyAdapterConfig
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

// ResolveOutputMode returns the effective output mode for the given adapter.
// If the adapter's platform has a recommended default (e.g. WeChat → summary),
// and the global mode is "verbose" (the default), the platform default is used.
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
		// WeChat iLink: context_token expires in ~5s, max 5 passive replies.
		// Force summary mode to ensure only one combined reply per inbound.
		if globalMode == "" || globalMode == "verbose" {
			return WechatDefaultOutputMode
		}
	}
	return globalMode
}
