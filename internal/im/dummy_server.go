package im

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// httpServer provides the HTTP endpoints for the dummy adapter.
type httpServer struct {
	adapter       *dummyAdapter
	sseBroker     *sseBroker
	shutdownToken string
	// cancel is set by start() (derived ctx) and fired by handleShutdown
	// once the bearer token verifies (#2959) - the endpoint used to be a
	// no-op that returned 200 without any shutdown side effect, leaving the
	// token chain (generate -> portFile -> eval script -> POST) dead code.
	cancel context.CancelFunc
}

type sseBroker struct {
	mu      sync.RWMutex
	buffer  []sseEntry
	bufSize int
	head    int // write position (ring)
	seq     int64
	subs    map[chan sseEntry]struct{}
}

type sseEntry struct {
	seq   int64
	event string // SSE event type
	data  []byte // JSON payload
}

func newHTTPServer(a *dummyAdapter) *httpServer {
	bufSize := dummyIntValue(a.cfg.Extra, "sse_buffer_size", 1024)
	token := generateShutdownToken()
	return &httpServer{
		adapter:       a,
		sseBroker:     newSSEBroker(bufSize),
		shutdownToken: token,
	}
}

func newSSEBroker(bufSize int) *sseBroker {
	if bufSize < 16 {
		bufSize = 16
	}
	return &sseBroker{
		buffer:  make([]sseEntry, bufSize),
		bufSize: bufSize,
		subs:    make(map[chan sseEntry]struct{}),
	}
}

func (b *sseBroker) push(eventType string, data []byte) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.seq++
	entry := sseEntry{seq: b.seq, event: eventType, data: data}

	// Write to ring buffer
	b.buffer[b.head] = entry
	b.head = (b.head + 1) % b.bufSize

	// Fan out to subscribers
	for ch := range b.subs {
		select {
		case ch <- entry:
		default:
			// subscriber too slow, drop
		}
	}
	return b.seq
}

func (b *sseBroker) subscribe() (chan sseEntry, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan sseEntry, 256)
	b.subs[ch] = struct{}{}
	return ch, b.seq
}

// replaySince returns buffered entries with seq strictly greater than
// since, oldest first (#2973). The ring holds at most the last bufSize
// entries: a since older than the window start still gets the full window
// (bounded at-least-once, matching hello's last_seq contract). Callers must
// subscribe BEFORE snapshotting so the live channel covers the gap after
// the snapshot; overlap is deduped by seq downstream.
func (b *sseBroker) replaySince(since int64) []sseEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	count := b.seq - since
	if count <= 0 {
		return nil
	}
	if count > int64(b.bufSize) {
		count = int64(b.bufSize)
	}
	start := (b.head - int(count) + b.bufSize) % b.bufSize
	out := make([]sseEntry, 0, count)
	for i := 0; i < int(count); i++ {
		idx := (start + i) % b.bufSize
		if b.buffer[idx].seq > since {
			out = append(out, b.buffer[idx])
		}
	}
	return out
}

func (b *sseBroker) unsubscribe(ch chan sseEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, ch)
}

// pushEvent converts an OutboundEvent to SSE and pushes it to the broker.
func (s *httpServer) pushEvent(event OutboundEvent) {
	sseType, data := outboundToSSE(event)
	if sseType == "" {
		return
	}
	s.sseBroker.push(sseType, data)
}

// outboundToSSE converts an OutboundEvent to an SSE event type and JSON data.
func outboundToSSE(event OutboundEvent) (string, []byte) {
	switch event.Kind {
	case OutboundEventText:
		text := event.Text
		if strings.HasPrefix(text, "🌙 ") {
			data, _ := json.Marshal(map[string]string{"kind": "knight_report", "content": text})
			return "knight_report", data
		}
		// Heuristic round_done detection
		if roundPattern.MatchString(text) {
			data, _ := json.Marshal(map[string]string{"kind": "round_done", "content": text})
			return "round_done", data
		}
		data, _ := json.Marshal(map[string]string{"kind": "text", "content": text})
		return "text", data

	case OutboundEventToolResult:
		if event.ToolRes == nil {
			return "", nil
		}
		data, _ := json.Marshal(map[string]interface{}{
			"kind":     "tool_result",
			"tool":     event.ToolRes.ToolName,
			"result":   event.ToolRes.Result,
			"is_error": event.ToolRes.IsError,
		})
		return "tool_result", data

	case OutboundEventApprovalRequest:
		data, _ := json.Marshal(map[string]interface{}{
			"kind": "approval_request",
		})
		return "approval_request", data

	case OutboundEventStatus:
		data, _ := json.Marshal(map[string]string{"kind": "status", "content": event.Status})
		return "status", data

	case OutboundEventToolCall:
		if event.ToolCall == nil {
			return "", nil
		}
		data, _ := json.Marshal(map[string]string{
			"kind":   "tool_call",
			"tool":   event.ToolCall.ToolName,
			"args":   event.ToolCall.Args,
			"detail": event.ToolCall.Detail,
		})
		return "tool_call", data
	}
	return "", nil
}

// handler returns the HTTP handler for all endpoints.
func (s *httpServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/send", s.handleSend)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/shutdown", s.handleShutdown)
	return mux
}

// start starts the HTTP server on the given address.
func (s *httpServer) start(ctx context.Context, listenAddr, portFile string) {
	// #2959: derive a cancellable child so POST /shutdown can trigger the
	// graceful stop path (srv.Close + listener.Close below) instead of only
	// the caller's external signal.
	ctx, s.cancel = context.WithCancel(ctx)
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		debug.Log("dummy", "listen failed: %v", err)
		return
	}

	srv := &http.Server{Handler: s.handler()}

	// Write port file atomically
	if portFile != "" {
		addr := listener.Addr().String()
		content := fmt.Sprintf("%s\n%s\n", addr, s.shutdownToken)
		tmpFile := portFile + ".tmp"
		if err := os.WriteFile(tmpFile, []byte(content), 0644); err == nil {
			os.Rename(tmpFile, portFile)
		}
	}

	debug.Log("dummy", "HTTP server listening on %s", listener.Addr())

	safego.Go("im.dummy.shutdown", func() {
		<-ctx.Done()
		srv.Close()
		listener.Close()
	})

	safego.Go("im.dummy.serve", func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			debug.Log("dummy", "server error: %v", err)
		}
	})
}

// handleSend processes POST /send requests.
// Returns immediately and runs HandleInbound in a background goroutine
// to avoid blocking the HTTP handler while the agent processes the message.
// This prevents deadlock when the agent triggers ask_user and the SSE
// consumer tries to reply via another /send request.
func (s *httpServer) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text            string `json:"text"`
		ClientMessageID string `json:"client_message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Reset metrics if requested
	if r.URL.Query().Get("reset_metrics") == "true" {
		s.adapter.metrics.Reset()
	}

	// Generate message ID
	msgID := req.ClientMessageID
	if msgID == "" {
		msgID = "msg_" + newID()
	}

	// Build InboundMessage
	msg := InboundMessage{
		Envelope: Envelope{
			Adapter:    s.adapter.name,
			Platform:   PlatformDummy,
			ChannelID:  "eval-channel",
			SenderID:   "eval-user",
			MessageID:  msgID,
			ReceivedAt: time.Now(),
		},
		Text: req.Text,
	}

	// Track user message count
	s.adapter.metrics.IncUserMessages()

	// Respond immediately so the caller (eval SSE consumer) can proceed
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "ok",
		"message_id": msgID,
	})

	// Submit through the manager in background to avoid blocking HTTP handler.
	// Use a fresh context detached from the HTTP request lifetime.
	safego.Go("im.dummy.handleInbound", func() {
		ctx := context.Background()
		if err := s.adapter.manager.HandleInbound(ctx, msg); err != nil {
			debug.Log("dummy", "HandleInbound error: %v", err)
		}
	})
}

// handleEvents serves the SSE event stream.
func (s *httpServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, lastSeq := s.sseBroker.subscribe()
	defer s.sseBroker.unsubscribe(ch)

	// Send hello with current seq
	helloData, _ := json.Marshal(map[string]int64{"last_seq": lastSeq})
	fmt.Fprintf(w, "event: hello\ndata: %s\n\n", helloData)
	flusher.Flush()

	// #2973: replay-on-reconnect. The SSE-standard Last-Event-ID header (or
	// ?since= query fallback) names the last event the client saw; entries
	// with seq > since are replayed from the ring buffer before the live
	// loop. subscribe() above already registered this connection, so no event
	// is lost between snapshot and live; entries arriving on BOTH paths are
	// deduped by seq (lastSent).
	since := lastSeq
	if h := strings.TrimSpace(r.Header.Get("Last-Event-ID")); h != "" {
		if v, err := strconv.ParseInt(h, 10, 64); err == nil {
			since = v
		}
	} else if q := strings.TrimSpace(r.URL.Query().Get("since")); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil {
			since = v
		}
	}
	var lastSent int64 = lastSeq
	if since < lastSeq {
		replayed := s.sseBroker.replaySince(since)
		for _, entry := range replayed {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", entry.event, entry.data)
			if entry.seq > lastSent {
				lastSent = entry.seq
			}
		}
		if len(replayed) > 0 {
			flusher.Flush()
		}
	}

	// Heartbeat ticker
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case entry := <-ch:
			if entry.seq <= lastSent {
				continue // already replayed from the snapshot (#2973)
			}
			lastSent = entry.seq
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", entry.event, entry.data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handleStatus returns current state snapshot.
func (s *httpServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := s.adapter.manager.Snapshot()
	response := map[string]interface{}{
		"bindings": snapshot.CurrentBindings,
		"metrics":  s.adapter.metrics.Snapshot(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleHealthz returns readiness check.
func (s *httpServer) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleShutdown gracefully cancels the context to trigger daemon shutdown.
func (s *httpServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Verify bearer token
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token != s.shutdownToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "shutting_down"})
	// #2959: fire the derived cancel - the stop goroutine in start()
	// observes ctx.Done() and closes srv+listener (graceful path). Guarded
	// for the pathological case of a request racing ahead of start().
	if s.cancel != nil {
		s.cancel()
	}
}

func generateShutdownToken() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405")))
	}
	return hex.EncodeToString(raw[:])
}
