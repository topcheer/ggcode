package im

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestDiscordIdentifyIntentsBitValue pins the intents bitmask (#945).
//
// Guilds=1<<0, GuildMessages=1<<9, GuildMessageReactions=1<<10,
// DirectMessages=1<<12, MessageContent=1<<15 → 1+512+1024+4096+32768 = 38401.
//
// Note: issue #945 / the fix request cite 38913 as the expected sum, but that
// arithmetic double-counts GuildMessages (38913 - 38401 = 512). The bitwise
// expression specified in the issue is authoritative and equals 38401.
func TestDiscordIdentifyIntentsBitValue(t *testing.T) {
	if discordIdentifyIntents != 38401 {
		t.Fatalf("discordIdentifyIntents = %d, want 38401 ((1<<0)|(1<<9)|(1<<10)|(1<<12)|(1<<15))", discordIdentifyIntents)
	}
	for _, bit := range []struct {
		name  string
		shift int
	}{
		{"Guilds", 0},
		{"GuildMessages", 9},
		{"GuildMessageReactions", 10},
		{"DirectMessages", 12},
		{"MessageContent", 15},
	} {
		if discordIdentifyIntents&(1<<bit.shift) == 0 {
			t.Errorf("discordIdentifyIntents missing %s bit (1<<%d)", bit.name, bit.shift)
		}
	}
	if discordOpResume != 6 {
		t.Errorf("discordOpResume = %d, want 6 (Gateway RESUME opcode)", discordOpResume)
	}
}

// fakeDiscordGateway is a minimal Discord Gateway v10 server that scripts a
// reconnect + RESUME flow across four connections:
//
//	conn 1: Hello → IDENTIFY → READY(session) → dispatch(s=7) → abrupt close
//	conn 2: Hello → RESUME(sess, seq=7) → RESUMED(s=8) → op 9 d=true
//	conn 3: Hello → RESUME(sess, ...)   → op 9 d=false (session dead)
//	conn 4: Hello → IDENTIFY (fresh session fallback)
type fakeDiscordGateway struct {
	srv *httptest.Server
	ops chan string // observed client ops, e.g. "IDENTIFY:38401", "RESUME:sess:7"
}

func newFakeDiscordGateway(t *testing.T) *fakeDiscordGateway {
	t.Helper()
	g := &fakeDiscordGateway{ops: make(chan string, 8)}
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()

	mux.HandleFunc("/gateway/bot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		wsBase := "ws" + strings.TrimPrefix(g.srv.URL, "http")
		_ = json.NewEncoder(w).Encode(map[string]any{"url": wsBase})
	})

	var connN int
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		connN++
		n := connN
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		send := func(op int, d any, s int, evType string) {
			payload := map[string]any{"op": op, "d": d}
			if s > 0 {
				payload["s"] = s
			}
			if evType != "" {
				payload["t"] = evType
			}
			b, _ := json.Marshal(payload)
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}
		readOp := func() (int, map[string]any) {
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return -1, nil
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return -1, nil
			}
			op := 0
			switch v := m["op"].(type) {
			case float64:
				op = int(v)
			}
			d, _ := m["d"].(map[string]any)
			return op, d
		}

		send(10, map[string]any{"heartbeat_interval": 41250}, 0, "") // Hello

		switch n {
		case 1:
			op, d := readOp()
			if op != 2 {
				g.ops <- fmt.Sprintf("conn1:want IDENTIFY(2), got op=%d", op)
				return
			}
			g.ops <- fmt.Sprintf("conn1:IDENTIFY:%v", d["intents"])
			send(0, map[string]any{"session_id": "sess-123", "user": map[string]any{"id": "42"}}, 1, "READY")
			send(0, map[string]any{"id": "g1"}, 7, "GUILD_CREATE") // bump sequence
			// abrupt close → client must reconnect with RESUME
		case 2:
			op, d := readOp()
			if op != 6 {
				g.ops <- fmt.Sprintf("conn2:want RESUME(6), got op=%d", op)
				return
			}
			g.ops <- fmt.Sprintf("conn2:RESUME:%v:%v", d["session_id"], d["seq"])
			send(0, map[string]any{"_trace": nil}, 8, "RESUMED")
			send(9, true, 0, "") // Invalid Session, resumable
		case 3:
			op, d := readOp()
			if op != 6 {
				g.ops <- fmt.Sprintf("conn3:want RESUME(6), got op=%d", op)
				return
			}
			g.ops <- fmt.Sprintf("conn3:RESUME:%v:%v", d["session_id"], d["seq"])
			send(9, false, 0, "") // Invalid Session, NOT resumable
		case 4:
			op, _ := readOp()
			if op != 2 {
				g.ops <- fmt.Sprintf("conn4:want IDENTIFY(2), got op=%d", op)
				return
			}
			g.ops <- "conn4:IDENTIFY:fresh"
		}
	})

	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func expectOp(t *testing.T, ops <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ops:
		if got != want {
			t.Fatalf("gateway observed %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for gateway to observe %q", want)
	}
}

// TestDiscordResumeStateMachine drives connectAndServe through a disconnect,
// a successful RESUME, an op 9 d=true (retry RESUME), and an op 9 d=false
// (fall back to IDENTIFY) - the full #947 state machine.
func TestDiscordResumeStateMachine(t *testing.T) {
	g := newFakeDiscordGateway(t)
	a := &discordAdapter{
		name:       "test",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		token:      "test-token",
		apiBase:    g.srv.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// conn 1: fresh IDENTIFY, READY stores session, abrupt close.
	if err := a.connectAndServe(ctx); err == nil {
		t.Fatalf("conn1: expected read error from abrupt close, got nil")
	}
	expectOp(t, g.ops, "conn1:IDENTIFY:38401")
	a.mu.RLock()
	sid, seq := a.sessionID, a.sequence
	a.mu.RUnlock()
	if sid != "sess-123" || seq != 7 {
		t.Fatalf("after conn1: sessionID=%q sequence=%d, want sess-123/7", sid, seq)
	}

	// conn 2: RESUME with stored session+seq, RESUMED, then op 9 d=true.
	if err := a.connectAndServe(ctx); err == nil {
		t.Fatalf("conn2: expected invalid-session error, got nil")
	}
	expectOp(t, g.ops, "conn2:RESUME:sess-123:7")
	a.mu.RLock()
	sid, seq = a.sessionID, a.sequence
	a.mu.RUnlock()
	if sid != "sess-123" {
		t.Fatalf("after op 9 d=true: sessionID=%q, want preserved sess-123", sid)
	}
	if seq != 8 {
		t.Fatalf("after op 9 d=true: sequence=%d, want 8 (RESUMED dispatch seq)", seq)
	}

	// conn 3: RESUME retried, then op 9 d=false clears session state.
	if err := a.connectAndServe(ctx); err == nil {
		t.Fatalf("conn3: expected invalid-session error, got nil")
	}
	expectOp(t, g.ops, "conn3:RESUME:sess-123:8")
	a.mu.RLock()
	sid, seq = a.sessionID, a.sequence
	a.mu.RUnlock()
	if sid != "" || seq != 0 {
		t.Fatalf("after op 9 d=false: sessionID=%q sequence=%d, want cleared", sid, seq)
	}

	// conn 4: fresh IDENTIFY fallback.
	if err := a.connectAndServe(ctx); err == nil {
		t.Fatalf("conn4: expected read error after server close, got nil")
	}
	expectOp(t, g.ops, "conn4:IDENTIFY:fresh")

	select {
	case extra := <-g.ops:
		t.Fatalf("unexpected extra gateway op: %q", extra)
	default:
	}
}
