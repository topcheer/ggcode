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
	sessionMu sync.RWMutex
	sessionID string
	nextEvent atomic.Int64

	// Send queue: all outbound messages go here.
	// The sender goroutine drains it continuously (no ACK blocking).
	// TCP/WebSocket guarantees ordered delivery.
	outMu    sync.Mutex
	outCond  *sync.Cond
	outbound []GatewayMessage // unbounded queue: enqueue never blocks
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
	subagentToolArgs map[string]string

	reasoningMu     sync.Mutex
	activeReasoning map[string]string // msgID -> agentID (empty for main agent)

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
	clientProjectionSeeded atomic.Bool
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

func NewBroker(sess *Session) *Broker {
	b := &Broker{
		session:          sess,
		sessionID:        newTunnelSessionID(),
		outDone:          make(chan struct{}),
		textBuf:          make(map[string]*textEntry),
		activeText:       make(map[string]*textEntry),
		textTick:         time.NewTicker(300 * time.Millisecond),
		textDone:         make(chan struct{}),
		sendWaiters:      make(map[string]chan struct{}),
		toolArgs:         make(map[string]string),
		subagentToolArgs: make(map[string]string),
		activeReasoning:  make(map[string]string),
	}
	b.outCond = sync.NewCond(&b.outMu)
	b.projectionCond = sync.NewCond(&b.projectionMu)

	// Start sender goroutine.
	go b.senderLoop()

	// Start text flush ticker
	go b.textFlushLoop()

	// Handle incoming messages from mobile
	sess.OnMessage(func(msg GatewayMessage) {
		if b.onCommand != nil {
			b.onCommand(msg)
		}
	})
	sess.OnConnected(func(info RelayConnectedState) {
		b.handleRelayConnected(info)
	})

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
			if err := b.session.Send(msg); err != nil {
				debug.Log("tunnel", "broker: send %s event=%s failed: %v", msg.Type, msg.EventID, err)
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

func (b *Broker) resetSession() string {
	b.sessionMu.Lock()
	defer b.sessionMu.Unlock()
	b.sessionID = newTunnelSessionID()
	b.nextEvent.Store(0)
	b.clientProjectionSeeded.Store(false)
	return b.sessionID
}

// ─── Session lifecycle ───

func (b *Broker) SendSessionInfo(data SessionInfoData) {
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
		b.nextEvent.Store(0)
		b.clientProjectionSeeded.Store(false)
	}
	b.sessionMu.Unlock()
	return changed
}

func (b *Broker) SwitchSession(sessionID string) {
	if !b.BindSession(sessionID) && strings.TrimSpace(sessionID) == "" {
		return
	}
	_ = b.session.SendActiveSession(sessionID)
	b.resetProjectionAndEnqueue(true)
}

func (b *Broker) AnnounceActiveSession(sessionID string) {
	if !b.BindSession(sessionID) && strings.TrimSpace(sessionID) == "" {
		return
	}
	_ = b.session.SendActiveSession(sessionID)
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
	msg := b.newMessage("sharing_stopped", "", nil)
	if msg.Type != "" {
		wait := b.trackSend(msg.EventID)
		b.enqueueOut(msg)
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
	b.Stop()
	if b.session != nil {
		b.session.DestroyGracefully(timeout)
	}
}

func (b *Broker) handleRelayConnected(info RelayConnectedState) {
	if b.onConnect != nil {
		b.onConnect(info)
	}
	currentSessionID := b.SessionID()
	if info.Role == "client" {
		// A newly joined mobile client should NOT force-reset existing clients when
		// the room already has retained history for the current active session.
		if b.clientProjectionSeeded.Load() && b.trustRelayHistory(info, currentSessionID) {
			b.bumpNextEvent(info.LastEventID)
			// Still flush any buffered live text so the joining client's resume replay
			// can observe the latest assistant chunks without resetting the room.
			b.flushAllText()
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
		if !b.beginClientReplaySync() {
			debug.Log("tunnel", "broker: coalescing duplicate client replay sync for session=%q count=%d", currentSessionID, info.HistoryCount)
			return
		}
		b.beginProjectionSync()
		go func() {
			defer b.endProjectionSync()
			defer b.endClientReplaySync()
			debug.Log("tunnel", "broker: client connected (relay session=%q count=%d local session=%q), publishing authoritative snapshot", info.SessionID, info.HistoryCount, currentSessionID)
			b.flushAllText()
			_ = b.session.SendActiveSession(b.SessionID())
			if replayed := b.replayCanonicalEvents(true); replayed {
				b.clientProjectionSeeded.Store(true)
				return
			}
			activeText := b.activeTextSnapshot()
			b.enqueueControl(EventSnapshotReset, nil)
			b.sendSnapshotDirect(snapshot)
			b.replayActiveText(activeText)
			b.clientProjectionSeeded.Store(true)
		}()
		return
	}
	if info.Role != "server" {
		return
	}
	if b.trustRelayHistory(info, currentSessionID) {
		b.bumpNextEvent(info.LastEventID)
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
	go func() {
		defer b.endProjectionSync()
		debug.Log("tunnel", "broker: relay state lost (relay session=%q count=%d local session=%q), reseeding snapshot", info.SessionID, info.HistoryCount, currentSessionID)
		b.flushAllText()
		if replayed := b.replayCanonicalEvents(false); replayed {
			return
		}
		activeText := b.activeTextSnapshot()
		b.sendSnapshotDirect(snapshot)
		b.replayActiveText(activeText)
	}()
}

func (b *Broker) beginClientReplaySync() bool {
	b.clientReplayMu.Lock()
	defer b.clientReplayMu.Unlock()
	if b.clientReplayInFlight {
		return false
	}
	b.clientReplayInFlight = true
	return true
}

func (b *Broker) endClientReplaySync() {
	b.clientReplayMu.Lock()
	b.clientReplayInFlight = false
	b.clientReplayMu.Unlock()
}

func (b *Broker) trustRelayHistory(info RelayConnectedState, currentSessionID string) bool {
	if info.SessionID != currentSessionID || info.HistoryCount == 0 {
		return false
	}
	b.snapshotMu.RLock()
	provider := b.replayProvider
	b.snapshotMu.RUnlock()
	if provider == nil {
		return true
	}
	events := provider()
	if len(events) == 0 {
		// During a live share the room can already hold authoritative retained
		// history even though local canonical replay is intentionally unavailable.
		// In that case, additional client joins should reuse relay history rather
		// than resetting existing ready clients with a fresh snapshot.
		return true
	}
	if len(events) != info.HistoryCount {
		return false
	}
	lastEventID := events[len(events)-1].EventID
	return lastEventID != "" && lastEventID == info.LastEventID
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
	b.activeReasoning[id] = ""
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
	}
	b.reasoningMu.Unlock()
	if !ok {
		return
	}
	if agentID != "" {
		b.enqueueWithStream(EventSubagentReasoningDone, id, SubagentTextData{AgentID: agentID, ID: id, Done: true})
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
	msg := b.newMessage(EventServerAck, "", AckData{MessageID: messageID})
	if msg.Type == "" {
		return
	}
	// server_ack is NOT recorded in event history — it's a transient signal.
	b.enqueueOut(msg)
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
		b.activeReasoning[msgID] = agentID
		b.reasoningMu.Unlock()
		b.enqueueWithStream(EventSubagentReasoning, msgID, SubagentTextData{
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
	if b.subagentToolArgs == nil {
		b.subagentToolArgs = make(map[string]string)
	}
	b.subagentToolArgs[fmt.Sprintf("%s:%s", agentID, streamID)] = args
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

func (b *Broker) PushSubagentToolResult(agentID, toolID, toolName, result string, isError bool) {
	b.waitProjectionSync()
	streamID := toolID
	if streamID == "" {
		streamID = fmt.Sprintf("%s-tool", agentID)
	}
	rawArgs := ""
	key := fmt.Sprintf("%s:%s", agentID, streamID)
	b.toolMu.Lock()
	rawArgs = b.subagentToolArgs[key]
	delete(b.subagentToolArgs, key)
	b.toolMu.Unlock()
	payload := SubagentToolResultData{
		AgentID: agentID, ToolID: toolID, ToolName: toolName, Result: result, IsError: isError,
	}
	if present, ok := toolpkg.DescribeToolResult(toolName, rawArgs, result, isError); ok {
		payload.Summary = present.Summary
		payload.Payload = present.Payload
		payload.PayloadMode = present.PayloadMode
	}
	b.enqueueWithStream(EventSubagentToolResult, streamID, SubagentToolResultData{
		AgentID: payload.AgentID, ToolID: payload.ToolID, ToolName: payload.ToolName, Result: payload.Result,
		Summary: payload.Summary, Payload: payload.Payload, PayloadMode: payload.PayloadMode, IsError: payload.IsError,
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
func (b *Broker) enqueueOut(msg GatewayMessage) {
	b.outMu.Lock()
	b.outbound = append(b.outbound, msg)
	b.outMu.Unlock()
	b.outCond.Signal()
}

func (b *Broker) enqueueRecorded(msg GatewayMessage) {
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
	msg := GatewayMessage{
		SessionID: b.SessionID(),
		EventID:   fmt.Sprintf("ev-%09d", b.nextEvent.Add(1)),
		StreamID:  ev.StreamID,
		Type:      ev.Type,
		Data:      append(json.RawMessage(nil), ev.Data...),
	}
	b.recordEvent(msg)
	b.enqueueOut(msg)
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
	msg := b.newMessage(eventType, "", data)
	if msg.Type == "" {
		return
	}
	b.recordEvent(msg)
	b.enqueueOut(msg)
}

func (b *Broker) enqueueWithStream(eventType, streamID string, data interface{}) {
	msg := b.newMessage(eventType, streamID, data)
	if msg.Type == "" {
		return
	}
	b.recordEvent(msg)
	b.enqueueOut(msg)
}

func (b *Broker) newMessage(eventType, streamID string, data interface{}) GatewayMessage {
	eventNum := b.nextEvent.Add(1)

	dataBytes, err := json.Marshal(data)
	if err != nil {
		debug.Log("tunnel", "broker: marshal error for %s: %v", eventType, err)
		return GatewayMessage{}
	}

	return GatewayMessage{
		SessionID: b.SessionID(),
		EventID:   fmt.Sprintf("ev-%09d", eventNum),
		StreamID:  streamID,
		Type:      eventType,
		Data:      dataBytes,
	}
}

func (b *Broker) recordEvent(msg GatewayMessage) {
	b.snapshotMu.RLock()
	recorder := b.eventRecorder
	b.snapshotMu.RUnlock()
	if recorder != nil {
		recorder(msg)
	}
}

func (b *Broker) replayCanonicalEvents(reset bool) bool {
	b.snapshotMu.RLock()
	provider := b.replayProvider
	b.snapshotMu.RUnlock()
	if provider == nil {
		return false
	}
	events := provider()
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
