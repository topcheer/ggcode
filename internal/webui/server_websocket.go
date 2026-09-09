package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleChatWS handles the WebSocket connection for chat.
// Protocol:
//
//	Client -> Server: {"type":"user_message","text":"..."}
//	Server -> Client: {"type":"text_delta","text":"..."}
//	Server -> Client: {"type":"tool_call","id":"...","name":"...","arguments":"..."}
//	Server -> Client: {"type":"tool_result","name":"...","result":"...","is_error":false}
//	Server -> Client: {"type":"done","usage":{"input_tokens":0,"output_tokens":0}}
//	Server -> Client: {"type":"error","error":"..."}

// GET /api/chat/history -- returns current agent conversation messages
func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var msgs []provider.Message
	switch {
	case s.chatBridge != nil:
		msgs = s.chatBridge.Messages()
	case s.agent != nil:
		s.agentMu.Lock()
		msgs = s.agent.Messages()
		s.agentMu.Unlock()
	default:
		writeJSON(w, []interface{}{})
		return
	}

	type contentBlock struct {
		Type     string          `json:"type"`
		Text     string          `json:"text,omitempty"`
		ToolName string          `json:"tool_name,omitempty"`
		ToolID   string          `json:"tool_id,omitempty"`
		Input    json.RawMessage `json:"input,omitempty"`
		Output   string          `json:"output,omitempty"`
		IsError  bool            `json:"is_error,omitempty"`
	}
	type message struct {
		Role    string         `json:"role"`
		Content []contentBlock `json:"content"`
	}
	result := make([]message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "system" {
			continue
		}
		blocks := make([]contentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			blocks = append(blocks, contentBlock{
				Type:     b.Type,
				Text:     b.Text,
				ToolName: b.ToolName,
				ToolID:   b.ToolID,
				Input:    b.Input,
				Output:   b.Output,
				IsError:  b.IsError,
			})
		}
		result = append(result, message{Role: m.Role, Content: blocks})
	}
	writeJSON(w, result)
}

func (s *Server) handleChatWS(w http.ResponseWriter, r *http.Request) {
	if s.chatBridge == nil && s.agent == nil {
		http.Error(w, "agent not available", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		debug.Log("webui", "ws upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// #927: half-open TCP connections (WiFi drop, NAT expiry - no FIN/RST)
	// kept the read loop blocked forever, leaking the subscription and
	// silently dropping events after writeCh filled. Heartbeat: pongs renew
	// the read deadline; a 30s ping ticker probes liveness; a missed
	// deadline fails ReadMessage and takes the existing cleanup path.
	const (
		wsReadTimeout  = 90 * time.Second
		wsPingInterval = 30 * time.Second
	)
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})
	pingDone := make(chan struct{})
	defer close(pingDone)
	safego.Go("webui.ws.pingLoop", func() {
		ticker := time.NewTicker(wsPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					return
				}
			case <-pingDone:
				return
			}
		}
	})

	// Dedicated write goroutine to serialize all WS writes.
	// Gorilla WebSocket requires read and write to be on different goroutines
	// and all writes serialized. This channel achieves both.
	writeCh := make(chan interface{}, 64)
	writeDone := make(chan struct{})
	safego.Go("webui.ws.writeLoop", func() {
		defer close(writeDone)
		for msg := range writeCh {
			if err := conn.WriteJSON(msg); err != nil {
				debug.Log("webui", "ws write error: %v", err)
				return
			}
		}
	})

	send := func(msg interface{}) {
		select {
		case writeCh <- msg:
		default:
			debug.Log("webui", "ws write channel full, dropping message")
		}
	}

	// #1857 case 2: terminal frames must NEVER be silently dropped. A
	// background-throttled tab fills the 64-deep channel with text deltas,
	// and the old non-blocking send then dropped the done/error frame too -
	// the UI waited forever with no marker. Terminal frames block up to 5s
	// (the write loop drains; a truly dead client is reaped by the ping/
	// read-deadline machinery) and delta drops are counted so the done
	// frame can carry a resync hint.
	deltaDrops := int64(0)
	sendTerminal := func(msg map[string]interface{}) {
		if msg["type"] == "done" && deltaDrops > 0 {
			msg["dropped_deltas"] = deltaDrops
			deltaDrops = 0
		}
		select {
		case writeCh <- msg:
		case <-time.After(5 * time.Second):
			debug.Log("webui", "ws write channel full on TERMINAL message, dropped after 5s")
		}
	}
	routeEvent := func(event provider.StreamEvent) {
		m := streamEventToJSON(event)
		// #1858 case 2: unmapped event types must be SKIPPED, not sent -
		// send(nil) made WriteJSON emit a literal `null` frame into the
		// protocol stream.
		if m == nil {
			return
		}
		switch m["type"] {
		case "text_delta", "tool_call_chunk":
			select {
			case writeCh <- m:
			default:
				atomic.AddInt64(&deltaDrops, 1)
				debug.Log("webui", "ws write channel full, dropping delta")
			}
		default: // done / error / tool_call / tool_result: terminal
			sendTerminal(m)
		}
	}

	// In bridge mode: subscribe immediately so all agent events are forwarded
	var unsub func()
	if s.chatBridge != nil {
		unsub = s.chatBridge.Subscribe(func(event provider.StreamEvent) {
			routeEvent(event)
		})
		// Note: unsub is called explicitly in the read-error path before
		// closing writeCh to avoid send-on-closed-channel panic. There is
		// no defer unsub() here because the return path handles it.
	}

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			// Unsubscribe BEFORE closing writeCh so no callback tries to
			// send on a closed channel (send-on-closed-channel panics).
			if unsub != nil {
				unsub()
			}
			close(writeCh)
			<-writeDone
			return
		}

		var msg struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Images []struct {
				MIME string `json:"mime"`
				Data string `json:"data"` // base64
			} `json:"images"`
			Files []struct {
				Name string `json:"name"`
				MIME string `json:"mime"`
				Data string `json:"data"` // base64
			} `json:"files"`
		}
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			send(map[string]interface{}{"type": "error", "error": "invalid message format"})
			continue
		}

		if msg.Type != "user_message" {
			send(map[string]interface{}{"type": "error", "error": "expected user_message"})
			continue
		}
		if msg.Text == "" && len(msg.Images) == 0 && len(msg.Files) == 0 {
			send(map[string]interface{}{"type": "error", "error": "message must contain text, images, or files"})
			continue
		}

		// Build content blocks
		content := []provider.ContentBlock{}
		if msg.Text != "" {
			content = append(content, provider.TextBlock(msg.Text))
		}
		for _, img := range msg.Images {
			if img.MIME == "" || img.Data == "" {
				continue
			}
			content = append(content, provider.ImageBlock(img.MIME, img.Data))
		}
		for _, f := range msg.Files {
			if f.Name == "" || f.Data == "" {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil {
				send(map[string]interface{}{"type": "error", "error": fmt.Sprintf("invalid base64 for file %s", f.Name)})
				continue
			}
			fileText := fmt.Sprintf("--- File: %s ---\n%s\n--- End of %s ---", f.Name, string(decoded), f.Name)
			content = append(content, provider.TextBlock(fileText))
		}

		// Route through bridge or direct agent BEFORE sending ack, so that
		// bridge state (e.g. lastContent) is populated before the client
		// reads the ack and checks it. This eliminates a test race where
		// the ack arrives faster than SendUserMessage completes.
		if s.chatBridge != nil {
			s.chatBridge.SendUserMessage(content)
		} else {
			// Legacy mode: directly run agent
			if !s.agentBusy.CompareAndSwap(false, true) {
				send(map[string]interface{}{"type": "error", "error": "agent is busy processing another request, please wait"})
				continue
			}
			s.agentMu.Lock()
			ctx, cancel := context.WithCancel(r.Context())
			done := make(chan struct{})
			readPumpDone := make(chan struct{})
			safego.Go("webui.ws.readPump", func() {
				defer close(readPumpDone)
				for {
					_, _, err := conn.ReadMessage()
					if err != nil {
						cancel()
						return
					}
				}
			})
			safego.Go("webui.ws.agentStream", func() {
				defer close(done)
				err := s.agent.RunStreamWithContent(ctx, content, func(event provider.StreamEvent) {
					defer safego.Recover("webui.ws.streamCallback")
					if m := streamEventToJSON(event); m != nil {
						send(m)
					}
				})
				if err != nil && ctx.Err() == nil {
					send(map[string]interface{}{"type": "error", "error": err.Error()})
				}
			})
			<-done
			cancel()
			// Force readPump's blocked ReadMessage to return immediately,
			// then wait for it to exit. Without this, the outer loop would
			// call ReadMessage concurrently with readPump — violating
			// gorilla/websocket's "no concurrent readers" contract.
			conn.SetReadDeadline(time.Now())
			<-readPumpDone
			// #1858 case 1: restore the #927 read deadline - clearing it
			// ("reset for outer loop") disabled half-open detection after
			// the first legacy round: the outer ReadMessage blocked forever,
			// pongs only renew on arrival, and ping WriteControl "succeeds"
			// into the kernel buffer of a dead peer. The goroutine and conn
			// leaked for the prototype #927 scenario after round one.
			conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
			s.agentMu.Unlock()
			s.agentBusy.Store(false)
		}

		// Send user acknowledgment with attachment info
		ackExtras := map[string]interface{}{"type": "user_ack", "text": msg.Text}
		if len(msg.Images) > 0 {
			ackExtras["image_count"] = len(msg.Images)
		}
		if len(msg.Files) > 0 {
			fileNames := make([]string, len(msg.Files))
			for i, f := range msg.Files {
				fileNames[i] = f.Name
			}
			ackExtras["file_names"] = fileNames
		}
		send(ackExtras)
	}
}

// streamEventToJSON converts a StreamEvent to a JSON-serializable map.
func streamEventToJSON(event provider.StreamEvent) map[string]interface{} {
	switch event.Type {
	case provider.StreamEventText:
		return map[string]interface{}{"type": "text_delta", "text": event.Text}
	case provider.StreamEventToolCallChunk:
		return map[string]interface{}{
			"type": "tool_call_chunk", "id": event.Tool.ID,
			"name": event.Tool.Name, "arguments": string(event.Tool.Arguments),
		}
	case provider.StreamEventToolCallDone:
		return map[string]interface{}{
			"type": "tool_call", "id": event.Tool.ID,
			"name": event.Tool.Name, "arguments": string(event.Tool.Arguments),
		}
	case provider.StreamEventToolResult:
		return map[string]interface{}{
			"type": "tool_result", "name": event.Tool.Name,
			"result": event.Result, "is_error": event.IsError,
		}
	case provider.StreamEventReasoning:
		// #1858 case 2: reasoning used to fall to default -> nil -> a
		// literal `null` frame written into the protocol stream, silently
		// losing DeepSeek/Anthropic thinking content. Forward it as a
		// collapsible reasoning_delta.
		return map[string]interface{}{"type": "reasoning_delta", "text": event.Text}
	case provider.StreamEventSystem:
		// #1858 case 2: system notices (retry/failover announcements) - map
		// to an info event instead of a null frame.
		return map[string]interface{}{"type": "system", "text": event.Text}
	case provider.StreamEventDone:
		doneMsg := map[string]interface{}{"type": "done"}
		if event.Usage != nil {
			doneMsg["usage"] = map[string]interface{}{
				"input_tokens":  event.Usage.InputTokens,
				"output_tokens": event.Usage.OutputTokens,
			}
		}
		return doneMsg
	case provider.StreamEventError:
		errMsg := "unknown error"
		if event.Error != nil {
			errMsg = event.Error.Error()
		}
		return map[string]interface{}{"type": "error", "error": errMsg}
	default:
		return nil
	}
}

// Dead code removed: wsSend, wsSendError, wsSendEvent were never called.
// All WebSocket writes go through the per-connection writeCh channel +
// dedicated write goroutine (lines 100-109), which properly serializes
// writes without needing s.mu.
