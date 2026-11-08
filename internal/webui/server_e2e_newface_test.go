package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// 本文件锁定四新增面的后端契约(P3 e2e 回归扩充):
//   1. 会话切换(idle 200 / busy 409 锁定)
//   2. WS 连接即推队列快照(syncQueue 修复依赖的契约)
//   3. 三能力命令提交(成功 200 / 失败 409)
//   4. 队列快照 HTTP 面(GET /api/chat/queue)
// busy 态说明:真实 busy 依赖 TUI 消息循环+LLM 调用,e2e 层以注入 fn 返回
// "agent busy" 错误锁定 409 分支——与 repl.RequestSessionSwitch 的锁定语义同形。

func newE2EServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := NewServer(config.DefaultConfig()) // 与既有 e2e 一致:WS 路径依赖非 nil cfg
	srv := httptest.NewServer(s.mux)
	t.Cleanup(srv.Close)
	return s, srv
}

func wsDial(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/chat/ws?token=" + token
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

// 用例1a:切换 fn 未注入 → 503(服务不可用,提示非 webui 模式)
func TestE2ESessionSwitchUnavailable(t *testing.T) {
	s, srv := newE2EServer(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sessions/switch",
		strings.NewReader(`{"id":"sess-1"}`))
	req.Header.Set("Authorization", "Bearer "+s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no fn: want 503, got %d", resp.StatusCode)
	}
}

// 用例1b:busy(锁定语义)→ 409,错误文案透传
func TestE2ESessionSwitchBusyLocks(t *testing.T) {
	s, srv := newE2EServer(t)
	s.SetSessionSwitchFn(func(id string) error { return errors.New("agent busy") })
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sessions/switch",
		strings.NewReader(`{"id":"sess-busy"}`))
	req.Header.Set("Authorization", "Bearer "+s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("busy: want 409, got %d", resp.StatusCode)
	}
	var body map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["error"].(string), "busy") {
		t.Fatalf("busy error should mention busy, got %v", body["error"])
	}
}

// 用例1c:idle 切换成功 → 200 且执行面收到正确会话 id
func TestE2ESessionSwitchIdleOK(t *testing.T) {
	s, srv := newE2EServer(t)
	got := ""
	s.SetSessionSwitchFn(func(id string) error { got = id; return nil })
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/sessions/switch",
		strings.NewReader(`{"id":"sess-target"}`))
	req.Header.Set("Authorization", "Bearer "+s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("idle: want 200, got %d", resp.StatusCode)
	}
	if got != "sess-target" {
		t.Fatalf("switch fn got %q, want sess-target", got)
	}
}

// 用例2:WS 连接即推队列快照(首条消息 type=queue event=list)——
// 前端 syncQueue 修复依赖此契约,后端改动若破坏此行为此用例即红
func TestE2EWSQueueSnapshotOnConnect(t *testing.T) {
	s, srv := newE2EServer(t)
	s.SetChatBridge(&mockChatBridge{}) // 握手要求 bridge/agent 至少其一非空
	s.SetQueueSnapshotFn(func() []string { return []string{"queued one", "queued two"} })
	ws := wsDial(t, srv, s.Token())

	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var first map[string]interface{}
	if err := ws.ReadJSON(&first); err != nil {
		t.Fatalf("read first message: %v", err)
	}
	if first["type"] != "queue" || first["event"] != "list" {
		t.Fatalf("first message should be queue snapshot, got %v", first)
	}
	items, _ := first["items"].([]interface{})
	if len(items) != 2 || items[0] != "queued one" {
		t.Fatalf("snapshot items = %v, want [queued one queued two]", items)
	}
	if first["count"].(float64) != 2 {
		t.Fatalf("snapshot count = %v, want 2", first["count"])
	}
}

// 用例3a:三能力命令提交失败 → 409(非静默:错误文案透传,TUI 侧另有 WS system 回流)
func TestE2EChatCommandFailureNonSilent(t *testing.T) {
	s, srv := newE2EServer(t)
	s.SetChatCommandFn(func(text string) error { return errors.New("agent busy") })
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/chat/commands",
		strings.NewReader(`{"text":"/effort high"}`))
	req.Header.Set("Authorization", "Bearer "+s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("failed command: want 409, got %d", resp.StatusCode)
	}
}

// 用例3b:合法命令 → 200 且命令文本原样抵达执行面(slash 全链入口契约)
func TestE2EChatCommandDispatch(t *testing.T) {
	s, srv := newE2EServer(t)
	received := ""
	s.SetChatCommandFn(func(text string) error { received = text; return nil })
	for _, cmd := range []string{"/model glm-5.3", "/effort high", "/mode bypass"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/chat/commands",
			strings.NewReader(`{"text":"`+cmd+`"}`))
		req.Header.Set("Authorization", "Bearer "+s.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", cmd, resp.StatusCode)
		}
		if received != cmd {
			t.Fatalf("dispatch got %q, want %q", received, cmd)
		}
	}
}

// 用例4:GET /api/chat/queue 快照面(前端切会话重拉依赖)
func TestE2EChatQueueSnapshotAPI(t *testing.T) {
	s, srv := newE2EServer(t)
	s.SetQueueSnapshotFn(func() []string { return []string{"only item"} })
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/chat/queue", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Items []string `json:"items"`
		Count int      `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 1 || len(body.Items) != 1 || body.Items[0] != "only item" {
		t.Fatalf("queue snapshot = %+v, want 1 item", body)
	}
}

// ===== 报障修链路(审计轮四修面)的可执行回归 =====
// 面界定:报障四修无独立 HTTP 端点;可执行回归锁定三条 WS 层契约——
// 非法 payload 非静默回错、合法提交触达派发面、反馈经 WS system 通道可达前端。

// 用例5:非法 payload(坏 JSON)→ WS 层非静默回 error 消息(4xx 的 WS 等价)
func TestE2EWSInvalidPayloadFeedback(t *testing.T) {
	s, srv := newE2EServer(t)
	s.SetChatBridge(&mockChatBridge{})
	ws := wsDial(t, srv, s.Token())
	// 先发上行再消费快照:helper 会阻塞读快照后下一条,发送必须在 skip 之前
	if err := ws.WriteMessage(websocket.TextMessage, []byte(`{broken json`)); err != nil {
		t.Fatal(err)
	}
	got := map[string]interface{}{}
	skipQueueSnapshot(t, ws, &got) // helper 返回时 got 已是首条非 queue 消息(error)
	if got["type"] != "error" || !strings.Contains(got["error"].(string), "invalid message format") {
		t.Fatalf("invalid payload should yield error feedback, got %v", got)
	}
}

// 用例6:合法 user_message → user_ack 且派发面(bridge.lastContent)收到完整文本
func TestE2EWSValidSubmitDispatchesBridge(t *testing.T) {
	s, srv := newE2EServer(t)
	bridge := &mockChatBridge{}
	s.SetChatBridge(bridge)
	ws := wsDial(t, srv, s.Token())
	// 先发上行再消费快照(发送必须在 skip 之前,否则 helper 阻塞等快照后下一条)
	if err := ws.WriteJSON(map[string]string{"type": "user_message", "text": "故障上报内容"}); err != nil {
		t.Fatal(err)
	}
	ack := map[string]interface{}{}
	skipQueueSnapshot(t, ws, &ack) // helper 返回时 ack 已是首条非 queue 消息(user_ack)
	if ack["type"] != "user_ack" || ack["text"] != "故障上报内容" {
		t.Fatalf("want user_ack echo, got %v", ack)
	}
	if len(bridge.lastContent) != 1 || bridge.lastContent[0].Text != "故障上报内容" {
		t.Fatalf("dispatch path not reached: lastContent = %v", bridge.lastContent)
	}
}

// 用例7:反馈经 WS system 通道可达前端(三能力失败反馈的通道契约:
// TUI 命令失败 chatWriteSystem → 系统消息回流;e2e 以 bridge 广播同型事件锁定通道)
func TestE2EWSBridgeSystemFeedbackReachesClient(t *testing.T) {
	s, srv := newE2EServer(t)
	bridge := &mockChatBridge{}
	s.SetChatBridge(bridge)
	ws := wsDial(t, srv, s.Token())
	// 广播先于 helper 启动(helper 阻塞读快照后下一条,后启动 goroutine 会死锁)
	go func() {
		time.Sleep(50 * time.Millisecond)
		bridge.broadcastEvent(provider.StreamEvent{Type: provider.StreamEventSystem, Text: "session.switch_blocked_running"})
	}()
	got := map[string]interface{}{}
	skipQueueSnapshot(t, ws, &got) // helper 返回时 got 已是首条非 queue 消息(system)
	if got["type"] != "system" || got["text"] != "session.switch_blocked_running" {
		t.Fatalf("system feedback should reach the client, got %v", got)
	}
}
