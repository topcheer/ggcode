package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

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

	// In bridge mode: subscribe immediately so all agent events are forwarded
	var unsub func()
	if s.chatBridge != nil {
		unsub = s.chatBridge.Subscribe(func(event provider.StreamEvent) {
			send(streamEventToJSON(event))
		})
		defer unsub()
	}

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
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

		// Route through bridge or direct agent
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
			safego.Go("webui.ws.readPump", func() {
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
					send(streamEventToJSON(event))
				})
				if err != nil && ctx.Err() == nil {
					send(map[string]interface{}{"type": "error", "error": err.Error()})
				}
			})
			<-done
			cancel()
			s.agentMu.Unlock()
			s.agentBusy.Store(false)
		}
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

func (s *Server) wsSend(conn *websocket.Conn, msg interface{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := conn.WriteJSON(msg); err != nil {
		debug.Log("webui", "ws write error: %v", err)
	}
}

func (s *Server) wsSendError(conn *websocket.Conn, errMsg string) {
	s.wsSend(conn, map[string]interface{}{"type": "error", "error": errMsg})
}

// wsSendEvent is used in legacy (non-bridge) mode only.
func (s *Server) wsSendEvent(conn *websocket.Conn, event provider.StreamEvent) {
	msg := streamEventToJSON(event)
	if msg != nil {
		s.wsSend(conn, msg)
	}
}
