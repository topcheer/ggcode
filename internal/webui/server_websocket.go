package webui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

// CheckOrigin 收紧(CSWSH 防护):同源放行;无 Origin 头(非浏览器客户端)放行;
// 代理感知同源——etunnel 等反代经 X-Forwarded-Host 还原公网 Host 后,Origin
// 与转发头匹配即视为同源;携带有效 query token 的跨源放行(纵深防御,攻击页
// 无法伪造 token);其余跨源拒绝。
func (s *Server) wsUpgrader() websocket.Upgrader {
	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			u, err := url.Parse(origin)
			if err != nil || u.Host == "" {
				return false
			}
			if u.Host == r.Host {
				return true
			}
			// 反代链只透传 X-Forwarded-Host(不回写 Host)时,与其匹配即同源
			if u.Host == r.Header.Get("X-Forwarded-Host") {
				return true
			}
			return s.authToken != "" && authTokenMatches(r.URL.Query().Get("token"), s.authToken)
		},
	}
}

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
	up := s.wsUpgrader()
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		debug.Log("webui", "ws upgrade error: %v", err)
		return
	}
	defer conn.Close()

	pingDone := s.startPingLoop(conn)
	defer close(pingDone)

	writeCh := make(chan interface{}, 64)
	writeDone := s.startWriteLoop(conn, writeCh)
	// 队列事件 sink 注册:断开时注销(快照在 send 定义后推送)
	unregisterQueue := s.registerQueueSink(writeCh)
	defer unregisterQueue()

	send := func(msg interface{}) {
		select {
		case writeCh <- msg:
		default:
			debug.Log("webui", "ws write channel full, dropping message")
		}
	}

	deltaDrops := int64(0)
	// 连接建立先推一次队列全量快照(重连/切会话后同步)
	send(s.queueSnapshotMsg())
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
		default:
			sendTerminal(m)
		}
	}

	var unsub func()
	if s.chatBridge != nil {
		unsub = s.chatBridge.Subscribe(func(event provider.StreamEvent) {
			routeEvent(event)
		})

	}

	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {

			if unsub != nil {
				unsub()
			}
			close(writeCh)
			<-writeDone
			return
		}

		msg, errMsg := parseUserMessage(msgBytes)
		if errMsg != "" {
			send(map[string]interface{}{"type": "error", "error": errMsg})
			continue
		}

		content := buildUserContent(msg, send)

		if s.chatBridge != nil {
			s.chatBridge.SendUserMessage(content)
		} else {
			if !s.runAgentMessage(r.Context(), conn, content, send) {
				continue
			}
		}

		send(userAck(msg))
	}
}

// wsUserMessage 浏览器上行消息(仅 user_message 类型)。
type wsUserMessage struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Images []struct {
		MIME string `json:"mime"`
		Data string `json:"data"`
	} `json:"images"`
	Files []struct {
		Name string `json:"name"`
		MIME string `json:"mime"`
		Data string `json:"data"`
	} `json:"files"`
}

const (
	wsReadTimeout  = 90 * time.Second
	wsPingInterval = 30 * time.Second
)

// startPingLoop 读超时+pong 续期+服务端保活 ping;返回关闭信号由调用方收尾。
func (s *Server) startPingLoop(conn *websocket.Conn) chan struct{} {
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})
	pingDone := make(chan struct{})
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
	return pingDone
}

// startWriteLoop 单写者泵;调用方 close(writeCh) 后等 writeDone 排空。
func (s *Server) startWriteLoop(conn *websocket.Conn, writeCh <-chan interface{}) chan struct{} {
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
	return writeDone
}

// parseUserMessage 反序列化+类型/非空校验;错误文案原样回给前端。
func parseUserMessage(msgBytes []byte) (*wsUserMessage, string) {
	var msg wsUserMessage
	if err := json.Unmarshal(msgBytes, &msg); err != nil {
		return nil, "invalid message format"
	}
	if msg.Type != "user_message" {
		return nil, "expected user_message"
	}
	if msg.Text == "" && len(msg.Images) == 0 && len(msg.Files) == 0 {
		return nil, "message must contain text, images, or files"
	}
	return &msg, ""
}

// buildUserContent 组装 ContentBlock(text/图片/文件);单个文件解码失败
// 发错误后继续处理其余文件(原 continue 在文件循环内的语义)。
func buildUserContent(msg *wsUserMessage, send func(interface{})) []provider.ContentBlock {
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
	return content
}

// runAgentMessage 直连 agent 分支(无 chatBridge):busy 拒绝返回 false(调用方
// 跳过 ack,与原 continue 语义一致)、读泵保活、流式回调转发、恢复读超时。
func (s *Server) runAgentMessage(requestCtx context.Context, conn *websocket.Conn, content []provider.ContentBlock, send func(interface{})) bool {
	if !s.agentBusy.CompareAndSwap(false, true) {
		send(map[string]interface{}{"type": "error", "error": "agent is busy processing another request, please wait"})
		return false
	}
	s.agentMu.Lock()
	ctx, cancel := context.WithCancel(requestCtx)
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
		}, "human")
		if err != nil && ctx.Err() == nil {
			send(map[string]interface{}{"type": "error", "error": err.Error()})
		}
	})
	<-done
	cancel()

	conn.SetReadDeadline(time.Now())
	<-readPumpDone

	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	s.agentMu.Unlock()
	s.agentBusy.Store(false)
	return true
}

// userAck 上行确认回执(文本回显+附件计数)。
func userAck(msg *wsUserMessage) map[string]interface{} {
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
	return ackExtras
}

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
	case provider.StreamEventSubagentDelta:
		sa := event.Subagent
		if sa == nil {
			return nil
		}
		return map[string]interface{}{
			"type": "subagent_delta", "id": sa.ID,
			"kind": sa.Kind, "text": sa.Text,
		}
	case provider.StreamEventSubagentDone:
		sa := event.Subagent
		if sa == nil {
			return nil
		}
		return map[string]interface{}{
			"type": "subagent_done", "id": sa.ID,
			"result": sa.Result, "is_error": sa.IsError,
		}
	case provider.StreamEventReasoning:

		return map[string]interface{}{"type": "reasoning_delta", "text": event.Text}
	case provider.StreamEventSystem:

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
