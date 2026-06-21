package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// Broker bridges agent events and the WebSocket tunnel protocol.
//
// Delivery guarantees:
//   - Text chunks are batched: PushText appends to a per-msgID buffer;
//     a 300ms ticker flushes all accumulated text as a single message.
//   - Every outbound message carries a stable session_id + event_id.
//   - The broker only produces ordered events for the current active session.
//   - Relay owns per-client replay; broker only emits the canonical event stream.
type Broker struct {
	session        *Session
	onCommand      func(cmd GatewayMessage)
	onConnect      func(info RelayConnectedState)
	replayProvider func() []GatewayMessage
	eventRecorder  func(msg GatewayMessage)

	// Session-scoped event identity.
	sessionMu         sync.RWMutex
	sessionID         string
	sessionGeneration uint64
	nextEvent         atomic.Int64
	cachedSessionInfo SessionInfoData

	// Send queue: all outbound messages go here.
	// The sender goroutine drains it continuously (no ACK blocking).
	// TCP/WebSocket guarantees ordered delivery.
	outMu    sync.Mutex
	outCond  *sync.Cond
	outbound []GatewayMessage // soft-capped queue (maxOutbound)
	outDone  chan struct{}
	stopOnce sync.Once

	waitMu      sync.Mutex
	sendWaiters map[string]chan struct{}

	snapshotMu       sync.RWMutex
	snapshotProvider func() BrokerSnapshot

	statusMu         sync.RWMutex
	currentStatus    StatusData
	hasCurrentStatus bool

	activityMu         sync.RWMutex
	currentActivity    ActivityData
	hasCurrentActivity bool

	toolMu           sync.Mutex
	toolArgs         map[string]string
	subagentToolMeta map[string]subagentToolMeta

	reasoningMu        sync.Mutex
	activeReasoning    map[string]string // msgID -> agentID (empty for main agent)
	activeReasoningBuf map[string]string // msgID -> accumulated reasoning text

	// Text batching
	textMu     sync.Mutex
	textBuf    map[string]*textEntry // msgID → unflushed text entry
	activeText map[string]*textEntry // msgID → full in-flight text entry
	textTick   *time.Ticker
	textDone   chan struct{} // stop text flusher

	projectionMu      sync.Mutex
	projectionCond    *sync.Cond
	projectionSyncing bool

	clientReplayMu         sync.Mutex
	clientReplayInFlight   bool
	activeClientReplay     *pendingClientReplay
	pendingClientReplay    *pendingClientReplay
	clientProjectionSeeded atomic.Bool
}

type subagentToolMeta struct {
	RawArgs     string
	DisplayName string
	Detail      string
}

type BrokerSnapshot struct {
	SessionInfo SessionInfoData
	History     []HistoryEntry
	Status      StatusData
	Activity    ActivityData
	ExtraEvents []SnapshotEvent
}

type SnapshotEvent struct {
	Type     string
	StreamID string
	Data     json.RawMessage
}

type pendingClientReplay struct {
	info       RelayConnectedState
	sessionID  string
	generation uint64
}

func NewBroker(sess *Session) *Broker {
	b := &Broker{
		session:            sess,
		sessionID:          newTunnelSessionID(),
		sessionGeneration:  1,
		outDone:            make(chan struct{}),
		textBuf:            make(map[string]*textEntry),
		activeText:         make(map[string]*textEntry),
		textTick:           time.NewTicker(300 * time.Millisecond),
		textDone:           make(chan struct{}),
		sendWaiters:        make(map[string]chan struct{}),
		toolArgs:           make(map[string]string),
		subagentToolMeta:   make(map[string]subagentToolMeta),
		activeReasoning:    make(map[string]string),
		activeReasoningBuf: make(map[string]string),
	}
	b.outCond = sync.NewCond(&b.outMu)
	b.projectionCond = sync.NewCond(&b.projectionMu)

	// Start sender goroutine.
	go b.senderLoop()

	// Start text flush ticker
	go b.textFlushLoop()

	// Handle incoming messages from mobile
	if sess != nil {
		sess.OnMessage(func(msg GatewayMessage) {
			if b.onCommand != nil {
				b.onCommand(msg)
			}
		})
		sess.OnConnected(func(info RelayConnectedState) {
			b.handleRelayConnected(info)
		})
	}

	return b
}

func newTunnelSessionID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return "sess-" + hex.EncodeToString(buf)
	}
	return fmt.Sprintf("sess-%d", time.Now().UnixNano())
}

// ─── Goroutines ───

// textEntry tracks accumulated text for a message stream.
// agentID == "" means main agent text; otherwise it's a subagent/teammate.
type textEntry struct {
	agentID string
	text    string
	kind    string
}

type reasoningEntry struct {
	agentID string
	text    string
}

// senderLoop drains the outbound queue. Never blocks the producer.
func (b *Broker) senderLoop() {
	for {
		b.outMu.Lock()
		for len(b.outbound) == 0 {
			select {
			case <-b.outDone:
				b.outMu.Unlock()
				return
			default:
			}
			b.outCond.Wait()
		}
		batch := b.outbound
		b.outbound = nil
		b.outMu.Unlock()

		for _, msg := range batch {
			if b.session != nil {
				if err := b.session.Send(msg); err != nil {
					debug.Log("tunnel", "broker: send %s event=%s failed: %v", msg.Type, msg.EventID, err)
				}
			}
			b.signalSent(msg.EventID)
		}
	}
}

// textFlushLoop periodically flushes accumulated text buffers to the sendQueue.
func (b *Broker) textFlushLoop() {
	for {
		select {
		case <-b.textTick.C:
			b.waitProjectionSync()
			b.flushAllText()
		case <-b.textDone:
			return
		}
	}
}

// flushAllText sends all accumulated text chunks as a single message per msgID.
func (b *Broker) flushAllText() {
	b.textMu.Lock()
	if len(b.textBuf) == 0 {
		b.textMu.Unlock()
		return
	}
	bufs := make(map[string]*textEntry)
	for k, v := range b.textBuf {
		if v.text != "" {
			bufs[k] = v
		}
	}
	for k := range b.textBuf {
		delete(b.textBuf, k)
	}
	b.textMu.Unlock()

	for msgID, entry := range bufs {
		if entry.agentID != "" {
			b.enqueueWithStream(EventSubagentText, msgID, SubagentTextData{AgentID: entry.agentID, ID: msgID, Chunk: entry.text})
		} else {
			b.enqueueWithStream(EventText, msgID, TextData{ID: msgID, Chunk: entry.text, Kind: entry.kind})
		}
	}
}

func (b *Broker) appendTextLocked(msgID, agentID, chunk, kind string) {
	if b.textBuf[msgID] == nil {
		b.textBuf[msgID] = &textEntry{agentID: agentID, kind: kind}
	}
	b.textBuf[msgID].text += chunk
	if kind != "" && b.textBuf[msgID].kind == "" {
		b.textBuf[msgID].kind = kind
	}
	if b.activeText[msgID] == nil {
		b.activeText[msgID] = &textEntry{agentID: agentID, kind: kind}
	}
	b.activeText[msgID].text += chunk
	if kind != "" && b.activeText[msgID].kind == "" {
		b.activeText[msgID].kind = kind
	}
}

// flushText flushes the buffer for a specific msgID immediately.
func (b *Broker) flushText(msgID string) {
	b.textMu.Lock()
	entry := b.textBuf[msgID]
	if entry == nil || entry.text == "" {
		b.textMu.Unlock()
		return
	}
	delete(b.textBuf, msgID)
	b.textMu.Unlock()

	if entry.agentID != "" {
		b.enqueueWithStream(EventSubagentText, msgID, SubagentTextData{AgentID: entry.agentID, ID: msgID, Chunk: entry.text})
	} else {
		b.enqueueWithStream(EventText, msgID, TextData{ID: msgID, Chunk: entry.text, Kind: entry.kind})
	}
}

func (b *Broker) activeTextSnapshot() map[string]textEntry {
	b.textMu.Lock()
	defer b.textMu.Unlock()
	if len(b.activeText) == 0 {
		return nil
	}
	out := make(map[string]textEntry, len(b.activeText))
	for id, entry := range b.activeText {
		if entry == nil || entry.text == "" {
			continue
		}
		out[id] = *entry
	}
	return out
}

func (b *Broker) replayActiveText(active map[string]textEntry) {
	if len(active) == 0 {
		return
	}
	ids := make([]string, 0, len(active))
	for id := range active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := active[id]
		if entry.agentID != "" {
			b.enqueueWithStream(EventSubagentText, id, SubagentTextData{AgentID: entry.agentID, ID: id, Chunk: entry.text})
		} else {
			b.enqueueWithStream(EventText, id, TextData{ID: id, Chunk: entry.text, Kind: entry.kind})
		}
	}
}

func (b *Broker) activeReasoningSnapshot() map[string]reasoningEntry {
	b.reasoningMu.Lock()
	defer b.reasoningMu.Unlock()
	if len(b.activeReasoningBuf) == 0 {
		return nil
	}
	out := make(map[string]reasoningEntry, len(b.activeReasoningBuf))
	for id, text := range b.activeReasoningBuf {
		if text == "" {
			continue
		}
		out[id] = reasoningEntry{
			agentID: b.activeReasoning[id],
			text:    text,
		}
	}
	return out
}

func (b *Broker) replayActiveReasoning(active map[string]reasoningEntry) {
	if len(active) == 0 {
		return
	}
	ids := make([]string, 0, len(active))
	for id := range active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := active[id]
		if entry.text == "" {
			continue
		}
		if entry.agentID != "" {
			b.enqueueWithStream(EventSubagentReasoning, id, SubagentReasoningData{
				AgentID: entry.agentID,
				ID:      id,
				Chunk:   entry.text,
			})
		} else {
			b.enqueueWithStream(EventReasoning, id, TextData{ID: id, Chunk: entry.text})
		}
	}
}

// ─── Lifecycle ───

func (b *Broker) OnCommand(fn func(cmd GatewayMessage)) {
	b.onCommand = fn
}

func (b *Broker) OnRelayConnected(fn func(info RelayConnectedState)) {
	b.onConnect = fn
}

func (b *Broker) SetSnapshotProvider(fn func() BrokerSnapshot) {
	b.snapshotMu.Lock()
	defer b.snapshotMu.Unlock()
	b.snapshotProvider = fn
}

func (b *Broker) SetReplayProvider(fn func() []GatewayMessage) {
	b.snapshotMu.Lock()
	defer b.snapshotMu.Unlock()
	b.replayProvider = fn
}

func (b *Broker) SetEventRecorder(fn func(msg GatewayMessage)) {
	b.snapshotMu.Lock()
	defer b.snapshotMu.Unlock()
	b.eventRecorder = fn
}

func (b *Broker) Stop() {
	b.stopOnce.Do(func() {
		b.textTick.Stop()
		close(b.textDone)
		close(b.outDone)
		b.outCond.Broadcast()
	})
}

func (b *Broker) SessionID() string {
	b.sessionMu.RLock()
	defer b.sessionMu.RUnlock()
	return b.sessionID
}

func (b *Broker) AuthorityEpoch() uint64 {
	b.sessionMu.RLock()
	defer b.sessionMu.RUnlock()
	if b.sessionGeneration == 0 {
		return 1
	}
	return b.sessionGeneration
}

func (b *Broker) sessionState() (string, uint64) {
	b.sessionMu.RLock()
	defer b.sessionMu.RUnlock()
	return b.sessionID, b.sessionGeneration
}

func (b *Broker) isSessionStateCurrent(sessionID string, generation uint64) bool {
	b.sessionMu.RLock()
	defer b.sessionMu.RUnlock()
	return b.sessionID == sessionID && b.sessionGeneration == generation
}

func (b *Broker) resetSession() string {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	b.sessionID = newTunnelSessionID()
	if b.sessionGeneration == 0 {
		b.sessionGeneration = 1
	} else {
		b.sessionGeneration++
	}
	b.nextEvent.Store(0)
	b.clientProjectionSeeded.Store(false)
	return b.sessionID
}

// ─── Session lifecycle ───

func (b *Broker) SendSessionInfo(data SessionInfoData) {
	// Don't let an empty Title overwrite a non-empty cached Title.
	// Replay events contain old session_info without Title; we must not
	// let them clobber the correct Title set by PrepareOnlineShare.
	if data.Title == "" && b.cachedSessionInfo.Title != "" {
		data.Title = b.cachedSessionInfo.Title
	}
	b.cachedSessionInfo = data
	b.enqueue(EventSessionInfo, data)
}

func (b *Broker) SendLanguageChange(lang string) {
	b.enqueue(EventLanguageChange, LanguageChangeData{Language: lang})
}

func (b *Broker) SendThemeChange(theme string) {
	b.enqueue(EventThemeChange, ThemeChangeData{Theme: theme})
}

func (b *Broker) SendSnapshot(snapshot BrokerSnapshot) {
	if snapshot.SessionInfo != (SessionInfoData{}) {
		// Preserve Title from cached session info — snapshot providers
		// (TUI/Desktop) don't always include Title, but PrepareOnlineShare
		// already set it correctly. Without this, the empty Title in the
		// snapshot overwrites the correct one.
		if snapshot.SessionInfo.Title == "" && b.cachedSessionInfo.Title != "" {
			snapshot.SessionInfo.Title = b.cachedSessionInfo.Title
		}
		b.SendSessionInfo(snapshot.SessionInfo)
	}
	if len(snapshot.History) > 0 {
		b.SeedHistory(snapshot.History)
	}
	for _, ev := range snapshot.ExtraEvents {
		b.enqueueSnapshotEvent(ev)
	}
	if snapshot.Status.Status != "" {
		b.statusMu.Lock()
		b.currentStatus = snapshot.Status
		b.hasCurrentStatus = true
		b.statusMu.Unlock()
		b.enqueue(EventStatus, snapshot.Status)
	}
	if snapshot.Activity.Activity != "" {
		b.activityMu.Lock()
		b.currentActivity = snapshot.Activity
		b.hasCurrentActivity = true
		b.activityMu.Unlock()
		b.enqueue(EventActivity, snapshot.Activity)
	}
}

// SendSnapshotFromProvider calls the snapshot provider (if set) and sends the
// resulting snapshot. Used by PrepareOnlineShare when there are no replay
// events to send.
func (b *Broker) SendSnapshotFromProvider() {
	b.snapshotMu.RLock()
	provider := b.snapshotProvider
	b.snapshotMu.RUnlock()
	if provider == nil {
		return
	}
	b.SendSnapshot(provider())
}

func (b *Broker) ResetSession() {
	b.resetSessionAndEnqueue(true)
}

func (b *Broker) BindSession(sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	b.sessionMu.Lock()
	changed := b.sessionID != sessionID
	b.sessionID = sessionID
	if changed {
		if b.sessionGeneration == 0 {
			b.sessionGeneration = 1
		} else {
			b.sessionGeneration++
		}
		b.nextEvent.Store(0)
		b.clientProjectionSeeded.Store(false)
	} else if b.sessionGeneration == 0 {
		b.sessionGeneration = 1
	}
	b.sessionMu.Unlock()
	return changed
}

func (b *Broker) SetAuthorityEpoch(epoch uint64) {
	if epoch == 0 {
		epoch = 1
	}
	b.sessionMu.Lock()
	b.sessionGeneration = epoch
	b.sessionMu.Unlock()
}

func (b *Broker) SwitchSession(sessionID string) {
	if !b.BindSession(sessionID) && strings.TrimSpace(sessionID) == "" {
		return
	}
	// Clear all broker-internal state from previous session so that
	// the new session starts from a clean slate.  The relay session
	// (encryption token, WebSocket connection) is preserved.
	b.toolMu.Lock()
	b.toolArgs = make(map[string]string)
	b.subagentToolMeta = make(map[string]subagentToolMeta)
	b.toolMu.Unlock()

	b.statusMu.Lock()
	b.currentStatus = StatusData{}
	b.hasCurrentStatus = false
	b.statusMu.Unlock()

	b.activityMu.Lock()
	b.currentActivity = ActivityData{}
	b.hasCurrentActivity = false
	b.activityMu.Unlock()

	b.clientReplayMu.Lock()
	b.clientReplayInFlight = false
	b.clientReplayMu.Unlock()

	b.sendActiveSession(sessionID)
	b.resetProjectionAndEnqueue(true)
	b.markRelayReady()
}

func (b *Broker) AnnounceActiveSession(sessionID string) {
	if !b.BindSession(sessionID) && strings.TrimSpace(sessionID) == "" {
		return
	}
	b.sendActiveSession(sessionID)
	b.markRelayReady()
}

func (b *Broker) activeSessionBarrier() (string, int64, string) {
	b.flushAllText()
	ordinal := b.nextEvent.Load()
	eventID := ""
	if ordinal > 0 {
		eventID = fmt.Sprintf("ev-%09d", ordinal)
	}
	return eventID, ordinal, ""
}

func (b *Broker) sendActiveSession(sessionID string) {
	if b == nil || b.session == nil {
		return
	}
	barrierEventID, barrierOrdinal, projectionHash := b.activeSessionBarrier()
	info := b.cachedSessionInfo
	_ = b.session.SendActiveSessionWithParams(sessionID, b.AuthorityEpoch(), barrierEventID, barrierOrdinal, projectionHash, info.Workspace, info.Provider, info.Model)
}

func (b *Broker) sendActiveSessionWithMode(sessionID, mode string) {
	if b == nil || b.session == nil {
		return
	}
	barrierEventID, barrierOrdinal, projectionHash := b.activeSessionBarrier()
	info := b.cachedSessionInfo
	_ = b.session.SendActiveSessionWithMode(sessionID, mode, b.AuthorityEpoch(), barrierEventID, barrierOrdinal, projectionHash, info.Workspace, info.Provider, info.Model)
}

func (b *Broker) markRelayReady() {
	if b == nil || b.session == nil {
		return
	}
	_ = b.session.SendServerReady(b.AuthorityEpoch())
}

func (b *Broker) resetSessionPreservingActiveText() {
	b.resetSessionAndEnqueue(false)
}

func (b *Broker) resetSessionAndEnqueue(clearActive bool) {
	b.resetSession()
	b.resetProjectionAndEnqueue(clearActive)
}

func (b *Broker) resetProjectionAndEnqueue(clearActive bool) {
	// Clear text buffers too.
	b.textMu.Lock()
	b.textBuf = make(map[string]*textEntry)
	if clearActive {
		b.activeText = make(map[string]*textEntry)
	}
	b.textMu.Unlock()
	b.reasoningMu.Lock()
	b.activeReasoning = make(map[string]string)
	b.activeReasoningBuf = make(map[string]string)
	b.reasoningMu.Unlock()
	b.enqueueControl(EventSnapshotReset, nil)
}

func (b *Broker) PushSharingStopped() {
	b.enqueue("sharing_stopped", nil)
}

func (b *Broker) StopSharingGracefully(timeout time.Duration) {
	if b == nil {
		return
	}
	b.flushAllText()
	dataBytes, err := json.Marshal(nil)
	if err == nil {
		msg, wait := b.enqueueWithBytes("sharing_stopped", "", dataBytes, false, true)
		if msg.Type != "" && wait != nil {
			timer := time.NewTimer(timeout)
			select {
			case <-wait:
			case <-timer.C:
				debug.Log("tunnel", "broker: timed out waiting to enqueue sharing_stopped event=%s", msg.EventID)
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
	b.Stop()
	if b.session != nil {
		b.session.DestroyGracefully(timeout)
	}
}

func (b *Broker) handleRelayConnected(info RelayConnectedState) {
	if b.onConnect != nil {
		b.onConnect(info)
	}
	currentSessionID, currentGeneration := b.sessionState()
	if info.Role == "client" {
		if info.ProtocolVersion != 0 && info.ProtocolVersion < ShareProtocolV3 {
			debug.Log("tunnel", "broker: rejecting unsupported relay client protocol=%d session=%q", info.ProtocolVersion, currentSessionID)
			return
		}
		// Fast path: if we already seeded and relay history matches, skip.
		if b.clientProjectionSeeded.Load() && b.trustRelayHistory(info, currentSessionID) {
			b.bumpNextEvent(info.LastEventID)
			b.flushAllText()
			return
		}
		b.snapshotMu.RLock()
		provider := b.snapshotProvider
		b.snapshotMu.RUnlock()
		if provider == nil {
			return
		}
		if !b.beginClientReplaySync(info, currentSessionID, currentGeneration) {
			debug.Log("tunnel", "broker: coalescing duplicate client replay sync for session=%q count=%d", currentSessionID, info.HistoryCount)
			return
		}
		b.beginProjectionSync()
		safego.Go("tunnel.broker.clientReplay", func() {
			defer b.endProjectionSync()
			defer b.endClientReplaySync()
			if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
				return
			}
			// Compute recovery plan inside goroutine to avoid blocking
			// the caller (handleRelayConnected runs on the readPump).
			plan, events := b.relayRecoveryPlan(info, currentSessionID)
			debug.Log("tunnel", "broker: client connected recovery plan trusted=%t reset=%t suffix_from=%d relay session=%q count=%d local session=%q", plan.trusted, plan.reset, plan.replayFrom, info.SessionID, info.HistoryCount, currentSessionID)
			if plan.trusted {
				// Relay already has everything — bump + flush, but we must
				// re-send session_info with the current encryption key.
				// Relay replay ciphertext uses the old key which the new
				// client cannot decrypt.
				b.bumpNextEvent(info.LastEventID)
				b.flushAllText()
				if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
					return
				}
				b.snapshotMu.RLock()
				provider := b.snapshotProvider
				b.snapshotMu.RUnlock()
				if provider != nil {
					if snapshot, ok := b.currentSnapshot(provider); ok {
						if snapshot.SessionInfo != (SessionInfoData{}) {
							b.enqueue(EventSessionInfo, snapshot.SessionInfo)
						}
					}
				}
				if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
					return
				}
				b.sendActiveSession(currentSessionID)
				b.enqueueWithStream(EventReplayDone, "", nil)
				b.clientProjectionSeeded.Store(true)
				return
			}
			b.flushAllText()
			if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
				return
			}
			b.sendActiveSession(currentSessionID)
			if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
				return
			}
			// Only replay the suffix that relay history doesn't have.
			suffix := events
			if plan.replayFrom < len(events) {
				suffix = events[plan.replayFrom:]
			} else {
				suffix = nil
			}
			if len(suffix) > 0 {
				if replayed := b.replayCanonicalEvents(true, suffix); replayed {
					b.enqueueControl(EventReplayDone, nil)
					b.clientProjectionSeeded.Store(true)
					return
				}
			}
			// No suffix to replay (or replay was empty) — send authoritative
			// snapshot with in-flight data so the client gets the full picture.
			if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
				return
			}
			activeText := b.activeTextSnapshot()
			activeReasoning := b.activeReasoningSnapshot()
			snapshot, ok := b.currentSnapshot(provider)
			if !ok {
				return
			}
			b.enqueueControl(EventSnapshotReset, nil)
			if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
				return
			}
			b.sendSnapshotDirect(snapshot)
			if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
				return
			}
			b.replayActiveText(activeText)
			if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
				return
			}
			b.replayActiveReasoning(activeReasoning)
			b.enqueueControl(EventReplayDone, nil)
			b.clientProjectionSeeded.Store(true)
		})
		return
	}
	if info.Role != "server" {
		return
	}
	plan, events := b.relayRecoveryPlan(info, currentSessionID)
	if plan.trusted {
		if info.LastEventID != "" {
			b.bumpNextEvent(info.LastEventID)
		}
		b.markRelayReady()
		// Always send active_session on server reconnect so the relay
		// has up-to-date workspace metadata. Even when the relay's
		// history is trusted, the room may have been restored from
		// SQLite without workspace fields (e.g. after relay restart).
		b.sendActiveSession(currentSessionID)
		return
	}
	b.snapshotMu.RLock()
	provider := b.snapshotProvider
	b.snapshotMu.RUnlock()
	if provider == nil {
		return
	}
	snapshot := provider()
	if snapshot.Status.Status == "" {
		if status, ok := b.CurrentStatus(); ok {
			snapshot.Status = status
		}
	}
	if snapshot.Activity.Activity == "" {
		if activity, ok := b.CurrentActivity(); ok {
			snapshot.Activity = activity
		}
	}
	if snapshot.SessionInfo == (SessionInfoData{}) && len(snapshot.History) == 0 && len(snapshot.ExtraEvents) == 0 && snapshot.Status.Status == "" && snapshot.Activity.Activity == "" {
		return
	}
	b.beginProjectionSync()
	safego.Go("tunnel.broker.relayReplay", func() {
		defer b.endProjectionSync()
		if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
			return
		}
		debug.Log("tunnel", "broker: relay recovery plan trusted=%t reset=%t suffix_from=%d relay session=%q count=%d local session=%q", plan.trusted, plan.reset, plan.replayFrom, info.SessionID, info.HistoryCount, currentSessionID)
		b.flushAllText()
		if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
			return
		}
		if plan.reset {
			b.sendActiveSessionWithMode(currentSessionID, ActiveSessionModeReplaceHistory)
		} else {
			b.sendActiveSession(currentSessionID)
		}
		if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
			return
		}
		if plan.replayFrom < len(events) {
			events = events[plan.replayFrom:]
		} else {
			events = nil
		}
		if replayed := b.replayCanonicalEvents(false, events); replayed {
			b.enqueueControl(EventReplayDone, nil)
			b.markRelayReady()
			return
		}
		if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
			return
		}
		activeText := b.activeTextSnapshot()
		activeReasoning := b.activeReasoningSnapshot()
		if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
			return
		}
		b.sendSnapshotDirect(snapshot)
		if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
			return
		}
		b.replayActiveText(activeText)
		if !b.isSessionStateCurrent(currentSessionID, currentGeneration) {
			return
		}
		b.replayActiveReasoning(activeReasoning)
		b.markRelayReady()
	})
}

func (b *Broker) beginClientReplaySync(info RelayConnectedState, sessionID string, generation uint64) bool {
	b.clientReplayMu.Lock()
	defer b.clientReplayMu.Unlock()
	next := &pendingClientReplay{
		info:       info,
		sessionID:  sessionID,
		generation: generation,
	}
	if b.clientReplayInFlight {
		if b.activeClientReplay != nil &&
			b.activeClientReplay.info == info &&
			b.activeClientReplay.sessionID == sessionID &&
			b.activeClientReplay.generation == generation {
			return false
		}
		b.pendingClientReplay = next
		return false
	}
	b.activeClientReplay = next
	b.pendingClientReplay = nil
	b.clientReplayInFlight = true
	return true
}

func (b *Broker) endClientReplaySync() {
	b.clientReplayMu.Lock()
	b.clientReplayInFlight = false
	b.activeClientReplay = nil
	pending := b.pendingClientReplay
	b.pendingClientReplay = nil
	b.clientReplayMu.Unlock()
	if pending == nil {
		return
	}
	go func(next pendingClientReplay) {
		b.waitProjectionSync()
		if !b.isSessionStateCurrent(next.sessionID, next.generation) {
			return
		}
		b.handleRelayConnected(next.info)
	}(*pending)
}

type relayRecoveryPlan struct {
	trusted    bool
	reset      bool
	replayFrom int
}

func (b *Broker) trustRelayHistory(info RelayConnectedState, currentSessionID string) bool {
	if info.SessionID != currentSessionID {
		return false
	}
	if info.AuthorityEpoch == 0 || info.AuthorityEpoch != b.AuthorityEpoch() {
		return false
	}
	events, available := b.canonicalReplayState()
	if !available {
		return false
	}
	if len(events) != info.HistoryCount {
		return false
	}
	if ProjectionHash(events) != strings.TrimSpace(info.ProjectionHash) {
		return false
	}
	if len(events) == 0 {
		return strings.TrimSpace(info.LastEventID) == ""
	}
	lastEventID := events[len(events)-1].EventID
	return lastEventID != "" && lastEventID == info.LastEventID
}

func (b *Broker) relayRecoveryPlan(info RelayConnectedState, currentSessionID string) (relayRecoveryPlan, []GatewayMessage) {
	events, available := b.canonicalReplayState()
	if !available {
		return relayRecoveryPlan{reset: info.HistoryCount > 0}, nil
	}
	currentAuthority := b.AuthorityEpoch()
	if info.AuthorityEpoch == 0 || info.AuthorityEpoch != currentAuthority {
		if len(events) == 0 && info.HistoryCount == 0 {
			return relayRecoveryPlan{trusted: true}, events
		}
		return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events
	}
	if info.SessionID != currentSessionID {
		return relayRecoveryPlan{reset: info.HistoryCount > 0, replayFrom: 0}, events
	}
	if len(events) == 0 {
		if info.HistoryCount == 0 && strings.TrimSpace(info.LastEventID) == "" && strings.TrimSpace(info.ProjectionHash) == "" {
			return relayRecoveryPlan{trusted: true}, nil
		}
		return relayRecoveryPlan{reset: true, replayFrom: 0}, nil
	}
	if info.HistoryCount == 0 {
		return relayRecoveryPlan{replayFrom: 0}, events
	}
	if info.HistoryCount > len(events) || info.LastEventID == "" {
		return relayRecoveryPlan{reset: true, replayFrom: 0}, events
	}
	prefixHash := ProjectionHashPrefix(events, info.HistoryCount)
	if prefixHash == "" || prefixHash != strings.TrimSpace(info.ProjectionHash) {
		return relayRecoveryPlan{reset: true, replayFrom: 0}, events
	}
	last := events[info.HistoryCount-1].EventID
	if last == "" || last != info.LastEventID {
		return relayRecoveryPlan{reset: true, replayFrom: 0}, events
	}
	if info.HistoryCount == len(events) {
		return relayRecoveryPlan{trusted: true}, events
	}
	return relayRecoveryPlan{replayFrom: info.HistoryCount}, events
}

func (b *Broker) currentSnapshot(provider func() BrokerSnapshot) (BrokerSnapshot, bool) {
	if provider == nil {
		return BrokerSnapshot{}, false
	}
	snapshot := provider()
	if snapshot.Status.Status == "" {
		if status, ok := b.CurrentStatus(); ok {
			snapshot.Status = status
		}
	}
	if snapshot.Activity.Activity == "" {
		if activity, ok := b.CurrentActivity(); ok {
			snapshot.Activity = activity
		}
	}
	if snapshot.SessionInfo == (SessionInfoData{}) && len(snapshot.History) == 0 && len(snapshot.ExtraEvents) == 0 && snapshot.Status.Status == "" && snapshot.Activity.Activity == "" {
		return BrokerSnapshot{}, false
	}
	return snapshot, true
}

// ─── User message ───

func (b *Broker) PushUserMessage(text string) {
	b.PushUserMessageData(MessageData{Text: text})
}

func (b *Broker) PushUserMessageData(data MessageData) {
	b.waitProjectionSync()
	b.enqueue(EventUserMessage, data)
}

func (b *Broker) PushSystemMessage(text string) {
	b.PushSystemMessageData(MessageData{Text: text})
}

func (b *Broker) PushSystemMessageData(data MessageData) {
	b.waitProjectionSync()
	b.enqueue(EventSystemMessage, data)
}

// ─── Streaming text (batched) ───

func (b *Broker) PushText(id, chunk string) {
	b.waitProjectionSync()
	b.textMu.Lock()
	b.appendTextLocked(id, "", chunk, "")
	b.textMu.Unlock()
}

func (b *Broker) PushTextData(data TextData) {
	b.waitProjectionSync()
	b.textMu.Lock()
	b.appendTextLocked(data.ID, "", data.Chunk, data.Kind)
	b.textMu.Unlock()
}

func (b *Broker) PushTextDone(id string) {
	b.waitProjectionSync()
	// Flush remaining text immediately, then send text_done
	b.flushText(id)
	b.enqueueWithStream(EventTextDone, id, TextData{ID: id, Done: true})
	b.textMu.Lock()
	delete(b.activeText, id)
	b.textMu.Unlock()
}

func (b *Broker) PushReasoning(id, chunk string) {
	b.waitProjectionSync()
	if strings.TrimSpace(id) == "" || chunk == "" {
		return
	}
	b.reasoningMu.Lock()
	if b.activeReasoning == nil {
		b.activeReasoning = make(map[string]string)
	}
	if b.activeReasoningBuf == nil {
		b.activeReasoningBuf = make(map[string]string)
	}
	b.activeReasoning[id] = ""
	b.activeReasoningBuf[id] += chunk
	b.reasoningMu.Unlock()
	b.enqueueWithStream(EventReasoning, id, TextData{ID: id, Chunk: chunk})
}

func (b *Broker) PushReasoningDone(id string) {
	b.waitProjectionSync()
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	b.reasoningMu.Lock()
	agentID, ok := b.activeReasoning[id]
	if ok {
		delete(b.activeReasoning, id)
		delete(b.activeReasoningBuf, id)
	}
	b.reasoningMu.Unlock()
	if !ok {
		return
	}
	if agentID != "" {
		b.enqueueWithStream(EventSubagentReasoningDone, id, SubagentReasoningData{AgentID: agentID, ID: id, Done: true})
		return
	}
	b.enqueueWithStream(EventReasoningDone, id, TextData{ID: id, Done: true})
}

// ─── Status ───

func (b *Broker) PushStatus(status, message string) {
	b.waitProjectionSync()
	b.statusMu.Lock()
	if b.hasCurrentStatus && b.currentStatus.Status == status && b.currentStatus.Message == message {
		b.statusMu.Unlock()
		return
	}
	b.currentStatus = StatusData{Status: status, Message: message}
	b.hasCurrentStatus = status != ""
	b.statusMu.Unlock()
	b.enqueue(EventStatus, StatusData{Status: status, Message: message})
}

func (b *Broker) CurrentStatus() (StatusData, bool) {
	b.statusMu.RLock()
	defer b.statusMu.RUnlock()
	return b.currentStatus, b.hasCurrentStatus
}

func (b *Broker) PushActivity(activity string) {
	b.waitProjectionSync()
	b.activityMu.Lock()
	if b.hasCurrentActivity && b.currentActivity.Activity == activity {
		b.activityMu.Unlock()
		return
	}
	b.currentActivity = ActivityData{Activity: activity}
	b.hasCurrentActivity = activity != ""
	b.activityMu.Unlock()
	b.enqueue(EventActivity, ActivityData{Activity: activity})
}

func (b *Broker) CurrentActivity() (ActivityData, bool) {
	b.activityMu.RLock()
	defer b.activityMu.RUnlock()
	return b.currentActivity, b.hasCurrentActivity
}

// ─── Tool calls ───

func (b *Broker) PushToolCall(toolID, toolName, displayName, args, detail string) {
	b.waitProjectionSync()
	if toolID != "" {
		b.toolMu.Lock()
		if b.toolArgs == nil {
			b.toolArgs = make(map[string]string)
		}
		b.toolArgs[toolID] = args
		b.toolMu.Unlock()
	}
	b.enqueueWithStream(EventToolCall, toolID, ToolCallData{
		ToolID:      toolID,
		ToolName:    toolName,
		DisplayName: displayName,
		Args:        args,
		Detail:      detail,
	})
}

func (b *Broker) PushToolResult(toolID, toolName, result string, isError bool) {
	b.waitProjectionSync()
	rawArgs := ""
	if toolID != "" {
		b.toolMu.Lock()
		rawArgs = b.toolArgs[toolID]
		delete(b.toolArgs, toolID)
		b.toolMu.Unlock()
	}
	payload := ToolResultData{ToolID: toolID, ToolName: toolName, Result: result, IsError: isError}
	if present, ok := toolpkg.DescribeToolResult(toolName, rawArgs, result, isError); ok {
		payload.Summary = present.Summary
		payload.Payload = present.Payload
		payload.PayloadMode = present.PayloadMode
	}
	b.enqueueWithStream(EventToolResult, toolID, payload)
}

// ─── Approval ───

func (b *Broker) PushApprovalRequest(id, toolName, input string) {
	b.waitProjectionSync()
	b.enqueueWithStream(EventApprovalRequest, id, ApprovalRequestData{ID: id, ToolName: toolName, Input: input})
}

func (b *Broker) PushApprovalResult(id, decision string) {
	b.waitProjectionSync()
	b.enqueueWithStream(EventApprovalResult, id, map[string]string{"id": id, "decision": decision})
}

// ─── Error ───

func (b *Broker) PushError(message string) {
	b.waitProjectionSync()
	b.enqueue(EventError, ErrorData{Message: message})
}

// ─── Ask User ───

func (b *Broker) PushServerAck(messageID string) {
	if messageID == "" {
		return
	}
	dataBytes, err := json.Marshal(AckData{MessageID: messageID})
	if err != nil {
		debug.Log("tunnel", "broker: marshal error for %s: %v", EventServerAck, err)
		return
	}
	// server_ack is NOT recorded in event history — it's a transient signal.
	b.enqueueWithBytes(EventServerAck, "", dataBytes, false, false)
}

func (b *Broker) PushAskUserRequest(id, title string, questions []AskUserQuestion) {
	b.waitProjectionSync()
	b.enqueueWithStream(EventAskUserRequest, id, AskUserRequestData{ID: id, Title: title, Questions: questions})
}

func (b *Broker) PushAskUserResponse(id, status string, answers []AskUserAnswer) {
	b.waitProjectionSync()
	b.enqueueWithStream(EventAskUserResponse, id, AskUserResponseData{ID: id, Status: status, Answers: answers})
}

// ─── Sub-agent / Teammate ───

func (b *Broker) PushSubagentSpawn(agentID, name, task, color, parentID string) {
	b.waitProjectionSync()
	b.enqueueWithStream(EventSubagentSpawn, agentID, SubagentSpawnData{
		AgentID: agentID, Name: name, Task: task, Color: color, ParentID: parentID,
	})
}

func (b *Broker) PushSubagentText(agentID, msgID, chunk string, done bool) {
	b.waitProjectionSync()
	if !done {
		b.textMu.Lock()
		b.appendTextLocked(msgID, agentID, chunk, "")
		b.textMu.Unlock()
	} else {
		b.flushText(msgID)
		b.textMu.Lock()
		delete(b.activeText, msgID)
		b.textMu.Unlock()
	}
}

func (b *Broker) PushSubagentReasoning(agentID, msgID, chunk string, done bool) {
	b.waitProjectionSync()
	if strings.TrimSpace(msgID) == "" {
		return
	}
	if chunk != "" {
		b.reasoningMu.Lock()
		if b.activeReasoning == nil {
			b.activeReasoning = make(map[string]string)
		}
		if b.activeReasoningBuf == nil {
			b.activeReasoningBuf = make(map[string]string)
		}
		b.activeReasoning[msgID] = agentID
		b.activeReasoningBuf[msgID] += chunk
		b.reasoningMu.Unlock()
		b.enqueueWithStream(EventSubagentReasoning, msgID, SubagentReasoningData{
			AgentID: agentID,
			ID:      msgID,
			Chunk:   chunk,
		})
	}
	if done {
		b.PushReasoningDone(msgID)
	}
}

func (b *Broker) PushSubagentStatus(agentID, status, message string) {
	b.waitProjectionSync()
	b.enqueueWithStream(EventSubagentStatus, agentID, SubagentStatusData{AgentID: agentID, Status: status, Message: message})
}

func (b *Broker) PushSubagentComplete(agentID, name, summary string, success bool) {
	b.waitProjectionSync()
	b.enqueueWithStream(EventSubagentComplete, agentID, SubagentCompleteData{
		AgentID: agentID, Name: name, Summary: summary, Success: success,
	})
}

func (b *Broker) PushSubagentToolCall(agentID, toolID, toolName, displayName, args, detail string) {
	b.waitProjectionSync()
	streamID := toolID
	if streamID == "" {
		streamID = fmt.Sprintf("%s-tool", agentID)
	}
	b.toolMu.Lock()
	if b.subagentToolMeta == nil {
		b.subagentToolMeta = make(map[string]subagentToolMeta)
	}
	b.subagentToolMeta[fmt.Sprintf("%s:%s", agentID, streamID)] = subagentToolMeta{
		RawArgs:     args,
		DisplayName: displayName,
		Detail:      detail,
	}
	b.toolMu.Unlock()
	b.enqueueWithStream(EventSubagentToolCall, streamID, SubagentToolCallData{
		AgentID:     agentID,
		ToolID:      toolID,
		ToolName:    toolName,
		DisplayName: displayName,
		Args:        args,
		Detail:      detail,
	})
}

func (b *Broker) PushSubagentToolResult(agentID, toolID, toolName, displayName, detail, result string, isError bool) {
	b.waitProjectionSync()
	streamID := toolID
	if streamID == "" {
		streamID = fmt.Sprintf("%s-tool", agentID)
	}
	meta := subagentToolMeta{}
	key := fmt.Sprintf("%s:%s", agentID, streamID)
	b.toolMu.Lock()
	meta = b.subagentToolMeta[key]
	delete(b.subagentToolMeta, key)
	b.toolMu.Unlock()
	if displayName == "" {
		displayName = meta.DisplayName
	}
	if detail == "" {
		detail = meta.Detail
	}
	payload := SubagentToolResultData{
		AgentID:     agentID,
		ToolID:      toolID,
		ToolName:    toolName,
		DisplayName: displayName,
		Detail:      detail,
		Result:      result,
		IsError:     isError,
	}
	if present, ok := toolpkg.DescribeToolResult(toolName, meta.RawArgs, result, isError); ok {
		payload.Summary = present.Summary
		payload.Payload = present.Payload
		payload.PayloadMode = present.PayloadMode
	}
	b.enqueueWithStream(EventSubagentToolResult, streamID, SubagentToolResultData{
		AgentID:     payload.AgentID,
		ToolID:      payload.ToolID,
		ToolName:    payload.ToolName,
		DisplayName: payload.DisplayName,
		Detail:      payload.Detail,
		Result:      payload.Result,
		Summary:     payload.Summary,
		Payload:     payload.Payload,
		PayloadMode: payload.PayloadMode,
		IsError:     payload.IsError,
	})
}

// ─── Utility ───

func (b *Broker) NextMessageID() string {
	return fmt.Sprintf("msg-%d", msgCount.Add(1))
}

var msgCount atomic.Int64

type HistoryEntry struct {
	Role        string `json:"role"`
	Content     string `json:"content"`
	DisplayText string `json:"display_text,omitempty"`
	Kind        string `json:"kind,omitempty"`
	// Tool fields (role == "tool_call" or "tool_result")
	ToolID          string `json:"tool_id,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	ToolDisplayName string `json:"tool_display_name,omitempty"`
	ToolArgs        string `json:"tool_args,omitempty"`
	ToolDetail      string `json:"tool_detail,omitempty"`
	Result          string `json:"result,omitempty"`
	IsError         bool   `json:"is_error,omitempty"`
}

func (b *Broker) SeedHistory(messages []HistoryEntry) {
	for _, entry := range messages {
		switch entry.Role {
		case "user":
			if entry.Content != "" {
				b.PushUserMessageData(MessageData{
					Text:        entry.Content,
					DisplayText: entry.DisplayText,
					Kind:        entry.Kind,
				})
			}
		case "system":
			if entry.Content != "" {
				b.PushSystemMessageData(MessageData{
					Text:        entry.Content,
					DisplayText: entry.DisplayText,
					Kind:        entry.Kind,
				})
			}
		case "assistant":
			if entry.Content == "" {
				continue
			}
			msgID := b.NextMessageID()
			b.enqueueWithStream(EventText, msgID, TextData{ID: msgID, Chunk: entry.Content, Kind: entry.Kind})
			b.enqueueWithStream(EventTextDone, msgID, TextData{ID: msgID, Done: true, Kind: entry.Kind})
		case "reasoning":
			if entry.Content == "" {
				continue
			}
			msgID := b.NextMessageID()
			b.enqueueWithStream(EventReasoning, msgID, TextData{ID: msgID, Chunk: entry.Content})
			b.enqueueWithStream(EventReasoningDone, msgID, TextData{ID: msgID, Done: true})
		case "tool_call":
			displayName := strings.TrimSpace(entry.ToolDisplayName)
			if displayName == "" {
				displayName = fallbackToolDisplayName(entry.ToolName)
			}
			detail := entry.ToolDetail
			if detail == "" && entry.ToolArgs != "" {
				present := toolpkg.DescribeTool(entry.ToolName, entry.ToolArgs)
				detail = present.Detail
			}
			b.PushToolCall(entry.ToolID, entry.ToolName, displayName, entry.ToolArgs, detail)
		case "tool_result":
			b.PushToolResult(entry.ToolID, entry.ToolName, entry.Result, entry.IsError)
		case "error":
			if entry.Content != "" {
				b.PushError(entry.Content)
			}
		}
	}
}

func fallbackToolDisplayName(toolName string) string {
	toolName = strings.ReplaceAll(toolName, "-", " ")
	toolName = strings.ReplaceAll(toolName, "_", " ")
	parts := strings.Fields(toolName)
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func (b *Broker) sendSnapshotDirect(snapshot BrokerSnapshot) {
	if snapshot.SessionInfo != (SessionInfoData{}) {
		b.enqueue(EventSessionInfo, snapshot.SessionInfo)
	}
	if len(snapshot.History) > 0 {
		b.seedHistoryDirect(snapshot.History)
	}
	for _, ev := range snapshot.ExtraEvents {
		b.enqueueSnapshotEvent(ev)
	}
	if snapshot.Status.Status != "" {
		b.statusMu.Lock()
		b.currentStatus = snapshot.Status
		b.hasCurrentStatus = true
		b.statusMu.Unlock()
		b.enqueue(EventStatus, snapshot.Status)
	}
	if snapshot.Activity.Activity != "" {
		b.activityMu.Lock()
		b.currentActivity = snapshot.Activity
		b.hasCurrentActivity = true
		b.activityMu.Unlock()
		b.enqueue(EventActivity, snapshot.Activity)
	}
}

func (b *Broker) seedHistoryDirect(messages []HistoryEntry) {
	for _, entry := range messages {
		switch entry.Role {
		case "user":
			if entry.Content != "" {
				b.enqueue(EventUserMessage, MessageData{
					Text:        entry.Content,
					DisplayText: entry.DisplayText,
					Kind:        entry.Kind,
				})
			}
		case "system":
			if entry.Content != "" {
				b.enqueue(EventSystemMessage, MessageData{
					Text:        entry.Content,
					DisplayText: entry.DisplayText,
					Kind:        entry.Kind,
				})
			}
		case "assistant":
			if entry.Content == "" {
				continue
			}
			msgID := b.NextMessageID()
			b.enqueueWithStream(EventText, msgID, TextData{ID: msgID, Chunk: entry.Content, Kind: entry.Kind})
			b.enqueueWithStream(EventTextDone, msgID, TextData{ID: msgID, Done: true, Kind: entry.Kind})
		case "reasoning":
			if entry.Content == "" {
				continue
			}
			msgID := b.NextMessageID()
			b.enqueueWithStream(EventReasoning, msgID, TextData{ID: msgID, Chunk: entry.Content})
			b.enqueueWithStream(EventReasoningDone, msgID, TextData{ID: msgID, Done: true})
		case "tool_call":
			displayName := strings.TrimSpace(entry.ToolDisplayName)
			if displayName == "" {
				displayName = fallbackToolDisplayName(entry.ToolName)
			}
			detail := entry.ToolDetail
			if detail == "" && entry.ToolArgs != "" {
				present := toolpkg.DescribeTool(entry.ToolName, entry.ToolArgs)
				detail = present.Detail
			}
			if entry.ToolID != "" {
				b.toolMu.Lock()
				if b.toolArgs == nil {
					b.toolArgs = make(map[string]string)
				}
				b.toolArgs[entry.ToolID] = entry.ToolArgs
				b.toolMu.Unlock()
			}
			b.enqueueWithStream(EventToolCall, entry.ToolID, ToolCallData{
				ToolID:      entry.ToolID,
				ToolName:    entry.ToolName,
				DisplayName: displayName,
				Args:        entry.ToolArgs,
				Detail:      detail,
			})
		case "tool_result":
			rawArgs := ""
			if entry.ToolID != "" {
				b.toolMu.Lock()
				rawArgs = b.toolArgs[entry.ToolID]
				delete(b.toolArgs, entry.ToolID)
				b.toolMu.Unlock()
			}
			payload := ToolResultData{
				ToolID:   entry.ToolID,
				ToolName: entry.ToolName,
				Result:   entry.Result,
				IsError:  entry.IsError,
			}
			if present, ok := toolpkg.DescribeToolResult(entry.ToolName, rawArgs, entry.Result, entry.IsError); ok {
				payload.Summary = present.Summary
				payload.Payload = present.Payload
				payload.PayloadMode = present.PayloadMode
			}
			b.enqueueWithStream(EventToolResult, entry.ToolID, payload)
		case "error":
			if entry.Content != "" {
				b.enqueue(EventError, ErrorData{Message: entry.Content})
			}
		}
	}
}

func (b *Broker) beginProjectionSync() {
	b.projectionMu.Lock()
	for b.projectionSyncing {
		b.projectionCond.Wait()
	}
	b.projectionSyncing = true
	b.projectionMu.Unlock()
}

func (b *Broker) endProjectionSync() {
	b.projectionMu.Lock()
	if b.projectionSyncing {
		b.projectionSyncing = false
		b.projectionCond.Broadcast()
	}
	b.projectionMu.Unlock()
}

func (b *Broker) waitProjectionSync() {
	b.projectionMu.Lock()
	for b.projectionSyncing {
		b.projectionCond.Wait()
	}
	b.projectionMu.Unlock()
}

// ─── Internal ───

// enqueueOut appends a message to the unbounded outbound queue and wakes the sender.
// This NEVER blocks, so OnUpdate callbacks can call tunnel methods safely.
const maxOutbound = 10000 // soft cap; older events dropped when exceeded

func (b *Broker) enqueueOut(msg GatewayMessage) {
	b.outMu.Lock()
	if len(b.outbound) >= maxOutbound {
		// Drop oldest 10% to make room, log the overflow.
		drop := maxOutbound / 10
		b.outbound = b.outbound[drop:]
		debug.Log("tunnel", "broker: outbound queue overflow, dropped %d events", drop)
	}
	b.outbound = append(b.outbound, msg)
	b.outMu.Unlock()
	b.outCond.Signal()
}

func (b *Broker) enqueueRecorded(msg GatewayMessage) {
	// When replaying old session_info events that lack Title (recorded
	// before Title was added to the protocol), fill in the cached Title
	// so mobile always receives the correct session title.
	if msg.Type == EventSessionInfo && b.cachedSessionInfo.Title != "" {
		var info SessionInfoData
		if err := json.Unmarshal(msg.Data, &info); err == nil && info.Title == "" {
			info.Title = b.cachedSessionInfo.Title
			if dataBytes, err := json.Marshal(info); err == nil {
				msg.Data = dataBytes
			}
		}
	}
	b.bumpNextEvent(msg.EventID)
	b.enqueueOut(msg)
}

func (b *Broker) enqueueControl(eventType string, data interface{}) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		debug.Log("tunnel", "broker: marshal control error for %s: %v", eventType, err)
		return
	}
	b.enqueueOut(GatewayMessage{
		SessionID: b.SessionID(),
		Type:      eventType,
		Data:      dataBytes,
	})
}

func (b *Broker) enqueueSnapshotEvent(ev SnapshotEvent) {
	if ev.Type == "" {
		return
	}
	data := append(json.RawMessage(nil), ev.Data...)
	b.outMu.Lock()
	eventNum := b.nextEvent.Add(1)
	msg := GatewayMessage{
		SessionID: b.SessionID(),
		EventID:   fmt.Sprintf("ev-%09d", eventNum),
		StreamID:  ev.StreamID,
		Type:      ev.Type,
		Data:      data,
	}
	b.outbound = append(b.outbound, msg)
	b.outMu.Unlock()
	b.outCond.Signal()
	b.recordEvent(msg)
}

func (b *Broker) trackSend(eventID string) <-chan struct{} {
	ch := make(chan struct{})
	b.waitMu.Lock()
	if b.sendWaiters == nil {
		b.sendWaiters = make(map[string]chan struct{})
	}
	b.sendWaiters[eventID] = ch
	b.waitMu.Unlock()
	return ch
}

func (b *Broker) signalSent(eventID string) {
	b.waitMu.Lock()
	ch := b.sendWaiters[eventID]
	delete(b.sendWaiters, eventID)
	b.waitMu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// enqueue assigns stable session/event metadata and puts on outbound.
func (b *Broker) enqueue(eventType string, data interface{}) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		debug.Log("tunnel", "broker: marshal error for %s: %v", eventType, err)
		return
	}
	b.enqueueWithBytes(eventType, "", dataBytes, true, false)
}

func (b *Broker) enqueueWithStream(eventType, streamID string, data interface{}) {
	// Marshal outside the lock — this is the expensive part.
	dataBytes, err := json.Marshal(data)
	if err != nil {
		debug.Log("tunnel", "broker: marshal error for %s: %v", eventType, err)
		return
	}
	b.enqueueWithBytes(eventType, streamID, dataBytes, true, false)
}

// enqueueWithBytes assigns an event ID and appends to the outbound queue under
// the same lock so queue order always matches event ID order.
func (b *Broker) enqueueWithBytes(eventType, streamID string, dataBytes []byte, record, waitForSend bool) (GatewayMessage, <-chan struct{}) {
	if eventType == "" {
		return GatewayMessage{}, nil
	}
	b.outMu.Lock()
	eventNum := b.nextEvent.Add(1)
	msg := GatewayMessage{
		SessionID:      b.SessionID(),
		EventID:        fmt.Sprintf("ev-%09d", eventNum),
		StreamID:       streamID,
		AuthorityEpoch: b.AuthorityEpoch(),
		Type:           eventType,
		Data:           dataBytes,
	}
	var wait <-chan struct{}
	if waitForSend {
		ch := make(chan struct{})
		b.waitMu.Lock()
		if b.sendWaiters == nil {
			b.sendWaiters = make(map[string]chan struct{})
		}
		b.sendWaiters[msg.EventID] = ch
		b.waitMu.Unlock()
		wait = ch
	}
	b.outbound = append(b.outbound, msg)
	b.outMu.Unlock()
	b.outCond.Signal()
	if record {
		b.recordEvent(msg)
	}
	return msg, wait
}

func (b *Broker) recordEvent(msg GatewayMessage) {
	b.snapshotMu.RLock()
	recorder := b.eventRecorder
	b.snapshotMu.RUnlock()
	if recorder != nil {
		recorder(msg)
	}
}

func (b *Broker) canonicalReplayEvents() []GatewayMessage {
	b.snapshotMu.RLock()
	provider := b.replayProvider
	b.snapshotMu.RUnlock()
	if provider == nil {
		return nil
	}
	return provider()
}

func (b *Broker) canonicalReplayState() ([]GatewayMessage, bool) {
	b.snapshotMu.RLock()
	provider := b.replayProvider
	b.snapshotMu.RUnlock()
	if provider == nil {
		return nil, false
	}
	events := provider()
	if events == nil {
		return nil, false
	}
	return events, true
}

func (b *Broker) replayCanonicalEvents(reset bool, events []GatewayMessage) bool {
	if len(events) == 0 {
		return false
	}
	if reset {
		b.resetProjectionAndEnqueue(false)
	}
	for _, msg := range events {
		b.enqueueRecorded(msg)
	}
	return true
}

func (b *Broker) ReplayEvents(events []GatewayMessage, reset bool) {
	if len(events) == 0 {
		return
	}
	if reset {
		b.resetProjectionAndEnqueue(false)
	}
	for _, msg := range events {
		b.enqueueRecorded(msg)
	}
	b.enqueueControl(EventReplayDone, nil)
}

func (b *Broker) PublishRecordedEvent(msg GatewayMessage) {
	b.waitProjectionSync()
	b.enqueueRecorded(msg)
}

func (b *Broker) PrimeEventIDs(events []GatewayMessage) {
	for _, msg := range events {
		b.bumpNextEvent(msg.EventID)
	}
}

func (b *Broker) bumpNextEvent(eventID string) {
	if eventID == "" {
		return
	}
	idx := strings.LastIndex(eventID, "-")
	raw := eventID
	if idx >= 0 {
		raw = eventID[idx+1:]
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return
	}
	for {
		cur := b.nextEvent.Load()
		if cur >= n {
			return
		}
		if b.nextEvent.CompareAndSwap(cur, n) {
			return
		}
	}
}
