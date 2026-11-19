package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Regression battery for issue #5805: a workspace rebind must drop the
// cached gateway session/seq so the next connection IDENTIFYs instead of
// RESUMEing stale state captured under the previous binding context, and the
// receive path must not discard messages whose seq is below the cached
// lastSeq (cursor gap).

func TestQQRebindDropsGatewaySessionState(t *testing.T) {
	adapter := &qqAdapter{name: "hermes", seen: map[string]time.Time{}}
	adapter.mu.Lock()
	adapter.sessionID = "sess-old"
	adapter.lastSeq = 42
	adapter.connected = true
	adapter.mu.Unlock()

	adapter.dropSessionOnRebind(PairingResult{
		Consumed:        true,
		Bound:           true,
		Kind:            PairingKindRebind,
		PreviousBinding: &ChannelBinding{Workspace: "/w", Adapter: "hermes", Platform: PlatformQQ, ChannelID: "group-1"},
		NewBinding:      &ChannelBinding{Workspace: "/w", Adapter: "hermes", Platform: PlatformQQ, ChannelID: "group-2"},
	})

	adapter.mu.RLock()
	sid, seq, connected := adapter.sessionID, adapter.lastSeq, adapter.connected
	adapter.mu.RUnlock()
	if sid != "" || seq != 0 || connected {
		t.Fatalf("expected session state dropped after rebind, got session=%q seq=%d connected=%v", sid, seq, connected)
	}
}

func TestQQNonRebindPairingKeepsGatewaySession(t *testing.T) {
	adapter := &qqAdapter{name: "hermes", seen: map[string]time.Time{}}
	adapter.mu.Lock()
	adapter.sessionID = "sess-old"
	adapter.lastSeq = 42
	adapter.connected = true
	adapter.mu.Unlock()

	// Initial bind (no previous binding), wrong-code reply, and empty results
	// must never drop gateway state.
	adapter.dropSessionOnRebind(PairingResult{
		Consumed:   true,
		Bound:      true,
		Kind:       PairingKindBind,
		NewBinding: &ChannelBinding{Workspace: "/w", Adapter: "hermes", Platform: PlatformQQ, ChannelID: "group-9"},
	})
	adapter.dropSessionOnRebind(PairingResult{Consumed: true, Kind: PairingKindRebind, ReplyText: "wrong code"})
	adapter.dropSessionOnRebind(PairingResult{})

	adapter.mu.RLock()
	sid, seq := adapter.sessionID, adapter.lastSeq
	adapter.mu.RUnlock()
	if sid != "sess-old" || seq != 42 {
		t.Fatalf("expected session state preserved for non-rebind results, got session=%q seq=%d", sid, seq)
	}
}

func TestQQLowSeqDispatchIsNotDiscarded(t *testing.T) {
	adapter := &qqAdapter{
		name:      "hermes",
		manager:   NewManager(),
		chatTypes: map[string]string{},
		seen:      map[string]time.Time{},
	}
	dispatch := func(id string, seq int) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{
			"op": 0,
			"s":  seq,
			"t":  "GROUP_AT_MESSAGE_CREATE",
			"d": map[string]any{
				"id":           id,
				"group_openid": "group-9",
				"content":      "hello " + id,
				"author":       map[string]any{"member_openid": "user-9", "username": "tester"},
			},
		})
		if err != nil {
			t.Fatalf("marshal dispatch: %v", err)
		}
		if err := adapter.handleGatewayPayload(context.Background(), raw); err != nil {
			t.Fatalf("handleGatewayPayload: %v", err)
		}
	}
	dispatch("msg-hi", 30)
	dispatch("msg-lo", 3)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		adapter.mu.RLock()
		_, hi := adapter.seen["msg-hi"]
		_, lo := adapter.seen["msg-lo"]
		seq := adapter.lastSeq
		adapter.mu.RUnlock()
		if hi && lo {
			if seq != 30 {
				t.Fatalf("lastSeq = %d, want monotonic max 30", seq)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("low-seq dispatch was discarded: receive path must not filter inbound events on lastSeq")
}

// recordingBridge5805 is a race-safe inbound bridge recorder.
type recordingBridge5805 struct {
	mu   sync.Mutex
	last InboundMessage
}

func (b *recordingBridge5805) SubmitInboundMessage(_ context.Context, msg InboundMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.last = msg
	return nil
}

func (b *recordingBridge5805) lastText() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last.Text
}

// fakeQQGateway5805 is a minimal QQ gateway that records the first op each
// connection sends after HELLO (identify vs resume), hands out a fresh
// session per connection, and replays a low-seq gap message on connection 2.
type fakeQQGateway5805 struct {
	srv *httptest.Server
	mu  sync.Mutex
	ops map[int][]string
}

func (g *fakeQQGateway5805) recordOp(conn int, op string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ops[conn] = append(g.ops[conn], op)
}

func (g *fakeQQGateway5805) opsSnapshot() map[int][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[int][]string, len(g.ops))
	for k, v := range g.ops {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (g *fakeQQGateway5805) hasOp(conn int, op string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, got := range g.ops[conn] {
		if got == op {
			return true
		}
	}
	return false
}

func (g *fakeQQGateway5805) hasOpPrefix(conn int, prefix string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, got := range g.ops[conn] {
		if strings.HasPrefix(got, prefix) {
			return true
		}
	}
	return false
}

func newFakeQQGateway5805(t *testing.T) *fakeQQGateway5805 {
	t.Helper()
	g := &fakeQQGateway5805{ops: make(map[int][]string)}
	upgrader := websocket.Upgrader{}
	connN := 0
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		connN++
		n := connN
		g.mu.Unlock()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		write := func(payload map[string]any) {
			b, err := json.Marshal(payload)
			if err != nil {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}
		read := func() (map[string]any, error) {
			_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return nil, err
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, err
			}
			return m, nil
		}
		drain := func() {
			for {
				if _, err := read(); err != nil {
					return
				}
			}
		}
		write(map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 600000}})
		first, err := read()
		if err != nil {
			g.recordOp(n, "read-error")
			return
		}
		op, _ := first["op"].(float64)
		switch op {
		case 2:
			g.recordOp(n, "identify")
		case 6:
			d, _ := first["d"].(map[string]any)
			g.recordOp(n, fmt.Sprintf("resume:%v:%v", d["session_id"], d["seq"]))
		default:
			g.recordOp(n, fmt.Sprintf("op-%v", op))
		}
		if op != 2 {
			return
		}
		write(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]any{"session_id": fmt.Sprintf("sess-%d", n)}})
		if n == 1 {
			// Bump the client cursor high under the original binding context.
			write(map[string]any{"op": 0, "s": 42, "t": "GUILD_MEMBER_LIST_UPDATE", "d": map[string]any{}})
			drain()
		}
		// Fresh server cursor after rebind: the gap message arrives with a seq
		// far below the pre-rebind lastSeq.
		write(map[string]any{"op": 0, "s": 7, "t": "GROUP_AT_MESSAGE_CREATE", "d": map[string]any{
			"id":           "msg-gap",
			"group_openid": "group-2",
			"content":      "gap message",
			"author":       map[string]any{"member_openid": "user-2", "username": "tester"},
		}})
		drain()
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func TestQQRebindForcesIdentifyNotResumeAndDeliversGap(t *testing.T) {
	gw := newFakeQQGateway5805(t)
	wsBase := "ws" + strings.TrimPrefix(gw.srv.URL, "http")

	mgr := NewManager()
	bridge := &recordingBridge5805{}
	mgr.SetBridge(bridge)
	_ = mgr.SetBindingStore(NewMemoryBindingStore())
	_ = mgr.SetPairingStore(NewMemoryPairingStore())
	mgr.BindSession(SessionBinding{SessionID: "session-1", Workspace: "/tmp/project-5805"})
	if _, err := mgr.BindChannel(ChannelBinding{
		Platform:  PlatformQQ,
		Adapter:   "hermes",
		TargetID:  "ops",
		ChannelID: "group-1",
	}); err != nil {
		t.Fatalf("BindChannel returned error: %v", err)
	}

	adapter := &qqAdapter{
		name:      "hermes",
		manager:   mgr,
		appID:     "123",
		appSecret: "secret",
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case qqTokenURL:
				return jsonResponse(`{"access_token":"token-123","expires_in":"3600"}`), nil
			case qqAPIBase + qqGatewayPath:
				return jsonResponse(`{"url":"` + wsBase + `"}`), nil
			default:
				if req.Method == http.MethodPost {
					return jsonResponse(`{}`), nil
				}
				t.Errorf("unexpected request URL: %s", req.URL.String())
				return nil, fmt.Errorf("unexpected request URL: %s", req.URL.String())
			}
		})},
		chatTypes:   map[string]string{},
		seen:        map[string]time.Time{},
		uploadCache: map[string]qqUploadCacheEntry{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { adapter.run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	})

	waitFor := func(desc string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if cond() {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s; ops=%v", desc, gw.opsSnapshot())
	}

	// Connection 1: IDENTIFY, capture sess-1, cursor climbs to 42.
	waitFor("initial identify", func() bool { return gw.hasOp(1, "identify") })
	waitFor("session captured", func() bool {
		adapter.mu.RLock()
		defer adapter.mu.RUnlock()
		return adapter.sessionID == "sess-1"
	})
	waitFor("cursor at 42", func() bool {
		seq, _ := adapter.sequence()
		return seq == 42
	})

	// Rebind the workspace from group-1 to group-2 through the real pairing
	// path (challenge, then code).
	adapter.handleMessageEvent(ctx, "GROUP_AT_MESSAGE_CREATE", map[string]any{
		"id":           "msg-p1",
		"group_openid": "group-2",
		"content":      "@bot rebind please",
		"author":       map[string]any{"member_openid": "user-2", "username": "tester"},
	})
	pending := mgr.Snapshot().PendingPairing
	if pending == nil || pending.Code == "" || pending.Kind != PairingKindRebind {
		t.Fatalf("expected pending rebind challenge, got %#v", pending)
	}
	adapter.handleMessageEvent(ctx, "GROUP_AT_MESSAGE_CREATE", map[string]any{
		"id":           "msg-p2",
		"group_openid": "group-2",
		"content":      pending.Code,
		"author":       map[string]any{"member_openid": "user-2", "username": "tester"},
	})

	// The rebind must synchronously drop the gateway session state.
	adapter.mu.RLock()
	sid, seq := adapter.sessionID, adapter.lastSeq
	adapter.mu.RUnlock()
	if sid != "" || seq != 0 {
		t.Fatalf("rebind must drop session/seq, got session=%q seq=%d; ops=%v", sid, seq, gw.opsSnapshot())
	}

	// After the forced reconnect, connection 2 must IDENTIFY, not RESUME.
	waitFor("second connection identify", func() bool { return gw.hasOp(2, "identify") })
	if gw.hasOpPrefix(2, "resume") {
		t.Fatalf("connection 2 sent RESUME after rebind: %v", gw.opsSnapshot())
	}

	// The gap message (seq 7 on the fresh cursor, below the pre-rebind 42)
	// must still be delivered to the bridge and recorded on the binding.
	waitFor("gap message delivered", func() bool { return bridge.lastText() == "gap message" })
	if binding := mgr.CurrentBinding(); binding == nil || binding.LastInboundMessageID != "msg-gap" {
		t.Fatalf("expected binding to record gap message, got %#v", binding)
	}
	waitFor("new session captured", func() bool {
		adapter.mu.RLock()
		defer adapter.mu.RUnlock()
		return adapter.sessionID == "sess-2"
	})
}
