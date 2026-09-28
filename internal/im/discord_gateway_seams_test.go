package im

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestExtractHeartbeatInterval pins the HELLO heartbeat-interval adoption
// rules: use the advertised value when present and positive, otherwise the
// Gateway v10 default of 41250ms.
func TestExtractHeartbeatInterval(t *testing.T) {
	cases := []struct {
		name string
		d    map[string]any
		want int
	}{
		{"nil payload defaults", nil, 41250},
		{"missing key defaults", map[string]any{}, 41250},
		{"valid interval adopted", map[string]any{"heartbeat_interval": 30000}, 30000},
		{"zero interval defaults", map[string]any{"heartbeat_interval": 0}, 41250},
		{"negative interval defaults", map[string]any{"heartbeat_interval": -5}, 41250},
		{"non-int value defaults", map[string]any{"heartbeat_interval": "fast"}, 41250},
		{"other keys ignored", map[string]any{"heartbeat_interval": 25000, "_trace": nil}, 25000},
	}
	for _, tc := range cases {
		if got := extractHeartbeatInterval(tc.d); got != tc.want {
			t.Errorf("%s: extractHeartbeatInterval = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestDiscordHandleInvalidSession pins op 9 semantics on the adapter state:
// d=true keeps sessionID/sequence for a RESUME retry on the next connection;
// d=false drops them so the next connection falls back to IDENTIFY (#947).
func TestDiscordHandleInvalidSession(t *testing.T) {
	newAdapter := func() *discordAdapter {
		return &discordAdapter{name: "test", sessionID: "sess-1", sequence: 42}
	}

	a := newAdapter()
	err := a.handleInvalidSession(map[string]any{"d": true})
	if err == nil || err.Error() != "invalid session (resumable=true)" {
		t.Fatalf("d=true: err = %v, want %q", err, "invalid session (resumable=true)")
	}
	a.mu.RLock()
	sid, seq := a.sessionID, a.sequence
	a.mu.RUnlock()
	if sid != "sess-1" || seq != 42 {
		t.Fatalf("d=true: sessionID=%q sequence=%d, want kept sess-1/42", sid, seq)
	}

	a = newAdapter()
	err = a.handleInvalidSession(map[string]any{"d": false})
	if err == nil || err.Error() != "invalid session (resumable=false)" {
		t.Fatalf("d=false: err = %v, want %q", err, "invalid session (resumable=false)")
	}
	a.mu.RLock()
	sid, seq = a.sessionID, a.sequence
	a.mu.RUnlock()
	if sid != "" || seq != 0 {
		t.Fatalf("d=false: sessionID=%q sequence=%d, want cleared /0", sid, seq)
	}

	// Missing / mistyped d defaults to resumable=false (dead session).
	a = newAdapter()
	_ = a.handleInvalidSession(map[string]any{})
	a.mu.RLock()
	sid = a.sessionID
	a.mu.RUnlock()
	if sid != "" {
		t.Fatalf("missing d: sessionID=%q, want cleared", sid)
	}
}

// TestDiscordRegisterConn pins the sequence-reset rule on (re)connect: the
// sequence is kept when a stored session ID exists (RESUME replay path) and
// cleared only for a brand-new session with no stored session ID (#947).
func TestDiscordRegisterConn(t *testing.T) {
	a := &discordAdapter{name: "test", sessionID: "sess-1", sequence: 7}
	a.registerConn(nil)
	a.mu.RLock()
	seq := a.sequence
	a.mu.RUnlock()
	if seq != 7 {
		t.Fatalf("stored session: sequence = %d, want kept 7", seq)
	}

	a = &discordAdapter{name: "test", sequence: 7}
	a.registerConn(nil)
	a.mu.RLock()
	seq = a.sequence
	a.mu.RUnlock()
	if seq != 0 {
		t.Fatalf("no stored session: sequence = %d, want cleared 0", seq)
	}
}

// TestDiscordConnectServeGatewayReconnectOp pins op 7 RECONNECT handling:
// the serve loop terminates with the gateway-requested error so run()
// reconnects and RESUMEs (Discord docs: reconnect can arrive at any point
// of the gateway connection lifecycle).
func TestDiscordConnectServeGatewayReconnectOp(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/gateway/bot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")
		_ = json.NewEncoder(w).Encode(map[string]any{"url": wsBase})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		b, _ := json.Marshal(map[string]any{"op": discordOpHello, "d": map[string]any{"heartbeat_interval": 41250}})
		_ = conn.WriteMessage(websocket.TextMessage, b)
		b, _ = json.Marshal(map[string]any{"op": discordOpReconnect})
		_ = conn.WriteMessage(websocket.TextMessage, b)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	a := &discordAdapter{
		name:       "test",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		token:      "t",
		apiBase:    srv.URL,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := a.connectAndServe(ctx)
	if err == nil || err.Error() != "gateway requested reconnect" {
		t.Fatalf("err = %v, want %q", err, "gateway requested reconnect")
	}
}

// TestDiscordConnectServeGatewayURLFailure pins the REST failure wrap: a
// gateway/bot API error surfaces as "get gateway URL: ..." without dialing.
func TestDiscordConnectServeGatewayURLFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "401: Unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	a := &discordAdapter{
		name:       "test",
		httpClient: srv.Client(),
		token:      "t",
		apiBase:    srv.URL,
	}
	err := a.connectAndServe(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "get gateway URL: Discord API [401]") {
		t.Fatalf("err = %v, want prefix %q", err, "get gateway URL: Discord API [401]")
	}
}
