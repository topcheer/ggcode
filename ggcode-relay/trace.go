package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	traceSuppressionWindow = 1500 * time.Millisecond
	traceFlushInterval     = 2 * time.Second
	traceStateTTL          = 5 * time.Minute
)

type relayTraceLogger struct {
	mu     sync.Mutex
	window time.Duration
	sink   func(string)
	states map[string]*relayTraceState
}

type relayTraceState struct {
	lastSeen    time.Time
	pendingTail string
	pendingSeen int
	sampledSeen int
}

func newRelayTraceLogger() *relayTraceLogger {
	return newRelayTraceLoggerWithSink(traceSuppressionWindow, func(line string) {
		log.Print(line)
	})
}

func newRelayTraceLoggerWithSink(window time.Duration, sink func(string)) *relayTraceLogger {
	if window <= 0 {
		window = traceSuppressionWindow
	}
	if sink == nil {
		sink = func(string) {}
	}
	return &relayTraceLogger{
		window: window,
		sink:   sink,
		states: make(map[string]*relayTraceState),
	}
}

func (l *relayTraceLogger) Log(key, summary string) {
	l.logAt(time.Now(), key, summary)
}

func (l *relayTraceLogger) LogImmediate(summary string) {
	if l == nil || l.sink == nil {
		return
	}
	l.sink(summary)
}

func (l *relayTraceLogger) LogEveryN(key, summary string, n int) {
	if l == nil || l.sink == nil || n <= 0 {
		return
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.states[key]
	if !ok {
		state = &relayTraceState{}
		l.states[key] = state
	}
	state.lastSeen = now
	state.sampledSeen++
	if state.sampledSeen%n != 0 {
		return
	}
	l.sink(fmt.Sprintf("%s sampled=%d", summary, state.sampledSeen))
}

func (l *relayTraceLogger) logAt(now time.Time, key, summary string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.states[key]
	if !ok {
		l.sink(summary)
		l.states[key] = &relayTraceState{lastSeen: now}
		return
	}

	if now.Sub(state.lastSeen) <= l.window {
		state.lastSeen = now
		state.pendingTail = summary
		state.pendingSeen++
		return
	}

	l.flushPendingLocked(state)
	l.sink(summary)
	state.lastSeen = now
}

func (l *relayTraceLogger) FlushAged() {
	l.flushAgedAt(time.Now())
}

func (l *relayTraceLogger) flushAgedAt(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for key, state := range l.states {
		if state.pendingSeen > 0 && now.Sub(state.lastSeen) > l.window {
			l.flushPendingLocked(state)
		}
		if state.pendingSeen == 0 && now.Sub(state.lastSeen) > traceStateTTL {
			delete(l.states, key)
		}
	}
}

func (l *relayTraceLogger) FlushAll() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, state := range l.states {
		l.flushPendingLocked(state)
	}
}

func (l *relayTraceLogger) flushPendingLocked(state *relayTraceState) {
	if state == nil || state.pendingSeen == 0 || state.pendingTail == "" {
		return
	}
	suppressed := state.pendingSeen - 1
	l.sink(fmt.Sprintf("%s tail=true suppressed=%d", state.pendingTail, suppressed))
	state.pendingTail = ""
	state.pendingSeen = 0
}

func (h *hub) traceRelayMessage(route, roomToken, clientID string, msg relayMessage, extra string) {
	if h == nil || h.tracer == nil {
		return
	}
	summary := traceMessageSummary(route, roomToken, clientID, msg, extra)
	keyClientID := clientID
	if keyClientID == "" {
		keyClientID = msg.ClientID
	}
	key := strings.Join([]string{
		route,
		shortToken(roomToken),
		shortTraceField(keyClientID),
		msg.Type,
		shortTraceField(msg.SessionID),
		shortTraceField(msg.EventID),
		shortTraceField(msg.StreamID),
		shortTraceField(msg.MessageID),
	}, "|")
	if isHeartbeatTraceMessage(msg) {
		h.tracer.LogEveryN(key, summary, 100)
		return
	}
	if !shouldSuppressTraceMessage(msg) {
		h.tracer.LogImmediate(summary)
		return
	}
	h.tracer.Log(key, summary)
}

func (h *hub) traceRoomEvent(route, roomToken, clientID string, ev roomEvent, extra string) {
	h.traceRelayMessage(route, roomToken, clientID, relayMessage{
		Type:      ev.typ,
		SessionID: ev.sessionID,
		EventID:   ev.eventID,
		StreamID:  ev.streamID,
	}, extra)
}

func (h *hub) flushTraceLogs() {
	if h == nil || h.tracer == nil {
		return
	}
	h.tracer.FlushAged()
}

func shouldSuppressTraceMessage(msg relayMessage) bool {
	return msg.Type == "encrypted"
}

func isHeartbeatTraceMessage(msg relayMessage) bool {
	return msg.Type == "ping" || msg.Type == "pong"
}

func traceMessageSummary(route, roomToken, clientID string, msg relayMessage, extra string) string {
	parts := []string{
		"[relay] trace",
		"route=" + route,
		"room=" + shortToken(roomToken),
		"type=" + emptyTraceField(msg.Type),
	}
	if clientID == "" {
		clientID = msg.ClientID
	}
	if clientID != "" {
		parts = append(parts, "client="+shortTraceField(clientID))
	}
	if msg.Role != "" {
		parts = append(parts, "role="+msg.Role)
	}
	if msg.SessionID != "" {
		parts = append(parts, "session="+shortTraceField(msg.SessionID))
	}
	if msg.EventID != "" {
		parts = append(parts, "event="+shortTraceField(msg.EventID))
	}
	if msg.StreamID != "" {
		parts = append(parts, "stream="+shortTraceField(msg.StreamID))
	}
	if msg.LastEventID != "" {
		parts = append(parts, "last="+shortTraceField(msg.LastEventID))
	}
	if msg.MessageID != "" {
		parts = append(parts, "message="+shortTraceField(msg.MessageID))
	}
	if msg.Count > 0 {
		parts = append(parts, fmt.Sprintf("count=%d", msg.Count))
	}
	if msg.ResumeMode != "" {
		parts = append(parts, "mode="+msg.ResumeMode)
	}
	if msg.Generation > 0 {
		parts = append(parts, fmt.Sprintf("generation=%d", msg.Generation))
	}
	if msg.RetryAfterMS > 0 {
		parts = append(parts, fmt.Sprintf("retry_after_ms=%d", msg.RetryAfterMS))
	}
	if msg.Reason != "" {
		parts = append(parts, "reason="+shortTraceField(msg.Reason))
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, " ")
}

func shortTraceField(v string) string {
	if len(v) <= 16 {
		return v
	}
	return v[:16]
}

func emptyTraceField(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func lastEventID(history []roomEvent) string {
	if len(history) == 0 {
		return ""
	}
	return history[len(history)-1].eventID
}
