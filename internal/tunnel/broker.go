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

	// Text batching
	textMu     sync.Mutex
	textBuf    map[string]*textEntry // msgID → unflushed text entry
	activeText map[string]*textEntry // msgID → full in-flight text entry
	textTick   *time.Ticker
	textDone   chan struct{} // stop text flusher
}

type BrokerSnapshot struct {
	SessionInfo SessionInfoData
	History     []HistoryEntry
	Status      StatusData
	ExtraEvents []SnapshotEvent
}

type SnapshotEvent struct {
	Type     string
	StreamID string
	Data     json.RawMessage
}

func NewBroker(sess *Session) *Broker {
	b := &Broker{
		session:     sess,
		sessionID:   newTunnelSessionID(),
		outDone:     make(chan struct{}),
		textBuf:     make(map[string]*textEntry),
		activeText:  make(map[string]*textEntry),
		textTick:    time.NewTicker(300 * time.Millisecond),
		textDone:    make(chan struct{}),
		sendWaiters: make(map[string]chan struct{}),
	}
	b.outCond = sync.NewCond(&b.outMu)

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
		b.PushStatus(snapshot.Status.Status, snapshot.Status.Message)
	}
}

func (b *Broker) ResetSession() {
	b.resetSessionAndEnqueue(true)
}

func (b *Broker) SwitchSession(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	b.sessionMu.Lock()
	changed := b.sessionID != sessionID
	b.sessionID = sessionID
	if changed {
		b.nextEvent.Store(0)
	}
	b.sessionMu.Unlock()
	_ = b.session.SendActiveSession(sessionID)
	b.resetProjectionAndEnqueue(true)
}

func (b *Broker) AnnounceActiveSession(sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	b.sessionMu.Lock()
	changed := b.sessionID != sessionID
	b.sessionID = sessionID
	if changed {
		b.nextEvent.Store(0)
	}
	b.sessionMu.Unlock()
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
		if b.trustRelayHistory(info, currentSessionID) {
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
		if snapshot.SessionInfo == (SessionInfoData{}) && len(snapshot.History) == 0 && len(snapshot.ExtraEvents) == 0 && snapshot.Status.Status == "" {
			return
		}
		go func() {
			debug.Log("tunnel", "broker: client connected (relay session=%q count=%d local session=%q), publishing authoritative snapshot", info.SessionID, info.HistoryCount, currentSessionID)
			b.flushAllText()
			_ = b.session.SendActiveSession(b.SessionID())
			if replayed := b.replayCanonicalEvents(true); replayed {
				return
			}
			activeText := b.activeTextSnapshot()
			b.enqueueControl(EventSnapshotReset, nil)
			b.SendSnapshot(snapshot)
			b.replayActiveText(activeText)
		}()
		return
	}
	if info.Role != "server" {
		return
	}
	if b.trustRelayHistory(info, currentSessionID) {
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
	if snapshot.SessionInfo == (SessionInfoData{}) && len(snapshot.History) == 0 && len(snapshot.ExtraEvents) == 0 && snapshot.Status.Status == "" {
		return
	}
	go func() {
		debug.Log("tunnel", "broker: relay state lost (relay session=%q count=%d local session=%q), reseeding snapshot", info.SessionID, info.HistoryCount, currentSessionID)
		b.flushAllText()
		if replayed := b.replayCanonicalEvents(false); replayed {
			return
		}
		activeText := b.activeTextSnapshot()
		b.SendSnapshot(snapshot)
		b.replayActiveText(activeText)
	}()
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
	b.enqueue(EventUserMessage, data)
}

func (b *Broker) PushSystemMessage(text string) {
	b.PushSystemMessageData(MessageData{Text: text})
}

func (b *Broker) PushSystemMessageData(data MessageData) {
	b.enqueue(EventSystemMessage, data)
}

// ─── Streaming text (batched) ───

func (b *Broker) PushText(id, chunk string) {
	b.textMu.Lock()
	b.appendTextLocked(id, "", chunk, "")
	b.textMu.Unlock()
}

func (b *Broker) PushTextData(data TextData) {
	b.textMu.Lock()
	b.appendTextLocked(data.ID, "", data.Chunk, data.Kind)
	b.textMu.Unlock()
}

func (b *Broker) PushTextDone(id string) {
	// Flush remaining text immediately, then send text_done
	b.flushText(id)
	b.enqueueWithStream(EventTextDone, id, TextData{ID: id, Done: true})
	b.textMu.Lock()
	delete(b.activeText, id)
	b.textMu.Unlock()
}

// ─── Status ───

func (b *Broker) PushStatus(status, message string) {
	b.statusMu.Lock()
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

// ─── Tool calls ───

func (b *Broker) PushToolCall(toolID, toolName, displayName, args, detail string) {
	b.enqueueWithStream(EventToolCall, toolID, ToolCallData{
		ToolID:      toolID,
		ToolName:    toolName,
		DisplayName: displayName,
		Args:        args,
		Detail:      detail,
	})
}

func (b *Broker) PushToolResult(toolID, toolName, result string, isError bool) {
	b.enqueueWithStream(EventToolResult, toolID, ToolResultData{ToolID: toolID, ToolName: toolName, Result: result, IsError: isError})
}

// ─── Approval ───

func (b *Broker) PushApprovalRequest(id, toolName, input string) {
	b.enqueueWithStream(EventApprovalRequest, id, ApprovalRequestData{ID: id, ToolName: toolName, Input: input})
}

func (b *Broker) PushApprovalResult(id, decision string) {
	b.enqueueWithStream(EventApprovalResult, id, map[string]string{"id": id, "decision": decision})
}

// ─── Error ───

func (b *Broker) PushError(message string) {
	b.enqueue(EventError, ErrorData{Message: message})
}

// ─── Ask User ───

func (b *Broker) PushAskUserRequest(id, title string, questions []AskUserQuestion) {
	b.enqueueWithStream(EventAskUserRequest, id, AskUserRequestData{ID: id, Title: title, Questions: questions})
}

func (b *Broker) PushAskUserResponse(id, status string, answers []AskUserAnswer) {
	b.enqueueWithStream(EventAskUserResponse, id, AskUserResponseData{ID: id, Status: status, Answers: answers})
}

// ─── Sub-agent / Teammate ───

func (b *Broker) PushSubagentSpawn(agentID, name, task, color, parentID string) {
	b.enqueueWithStream(EventSubagentSpawn, agentID, SubagentSpawnData{
		AgentID: agentID, Name: name, Task: task, Color: color, ParentID: parentID,
	})
}

func (b *Broker) PushSubagentText(agentID, msgID, chunk string, done bool) {
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

func (b *Broker) PushSubagentStatus(agentID, status, message string) {
	b.enqueueWithStream(EventSubagentStatus, agentID, SubagentStatusData{AgentID: agentID, Status: status, Message: message})
}

func (b *Broker) PushSubagentComplete(agentID, name, summary string, success bool) {
	b.enqueueWithStream(EventSubagentComplete, agentID, SubagentCompleteData{
		AgentID: agentID, Name: name, Summary: summary, Success: success,
	})
}

func (b *Broker) PushSubagentToolCall(agentID, toolID, toolName, displayName, args, detail string) {
	streamID := toolID
	if streamID == "" {
		streamID = fmt.Sprintf("%s-tool", agentID)
	}
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
	streamID := toolID
	if streamID == "" {
		streamID = fmt.Sprintf("%s-tool", agentID)
	}
	b.enqueueWithStream(EventSubagentToolResult, streamID, SubagentToolResultData{
		AgentID: agentID, ToolID: toolID, ToolName: toolName, Result: result, IsError: isError,
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
