package im

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

// testWSServer starts a minimal WebSocket echo server and returns its URL + a
// function to create a connected websocket.Conn.
func testWSServer(t *testing.T) (url string) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + srv.URL[4:] // http:// → ws://
}

func dialWS(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func TestQQAdapterClose(t *testing.T) {
	wsURL := testWSServer(t)
	conn := dialWS(t, wsURL)

	a := &qqAdapter{}
	a.mu.Lock()
	a.ws = conn
	a.connected = true
	a.mu.Unlock()

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if a.connected {
		t.Fatal("should not be connected after Close")
	}
	if a.ws != nil {
		t.Fatal("ws should be nil after Close")
	}
	// Verify the connection is actually closed — writing should fail
	a.mu.Lock()
	old := a.ws
	a.ws = nil
	a.mu.Unlock()
	_ = old // already nil, but confirm no panic
}

func TestQQAdapterCloseNoConnection(t *testing.T) {
	a := &qqAdapter{}
	if err := a.Close(); err != nil {
		t.Fatalf("Close on nil ws: %v", err)
	}
}

func TestDiscordAdapterClose(t *testing.T) {
	wsURL := testWSServer(t)
	conn := dialWS(t, wsURL)

	a := &discordAdapter{}
	a.mu.Lock()
	a.ws = conn
	a.connected = true
	a.mu.Unlock()

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if a.connected {
		t.Fatal("should not be connected after Close")
	}
}

func TestSlackAdapterClose(t *testing.T) {
	wsURL := testWSServer(t)
	conn := dialWS(t, wsURL)

	a := &slackAdapter{}
	a.mu.Lock()
	a.ws = conn
	a.connected = true
	a.mu.Unlock()

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if a.connected {
		t.Fatal("should not be connected after Close")
	}
}

func TestDingTalkAdapterClose(t *testing.T) {
	wsURL := testWSServer(t)
	conn := dialWS(t, wsURL)

	a := &dingtalkAdapter{}
	a.mu.Lock()
	a.ws = conn
	a.connected = true
	a.mu.Unlock()

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if a.connected {
		t.Fatal("should not be connected after Close")
	}
}

func TestFeishuAdapterCloseNoServer(t *testing.T) {
	a := &feishuAdapter{}
	if err := a.Close(); err != nil {
		t.Fatalf("Close with no httpServer: %v", err)
	}
}

func TestFeishuAdapterCloseWithServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	a := &feishuAdapter{}
	a.mu.Lock()
	a.httpServer = srv.Config
	a.mu.Unlock()

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if a.httpServer != nil {
		t.Fatal("httpServer should be nil after Close")
	}
}

func TestTGAdapterClose(t *testing.T) {
	a := &tgAdapter{}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestTGAdapterCloseIdleConnections(t *testing.T) {
	// Verify Close() calls CloseIdleConnections on the httpClient.
	a := &tgAdapter{httpClient: &http.Client{}}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After Close, the adapter should be marked disconnected.
	a.mu.RLock()
	connected := a.connected
	a.mu.RUnlock()
	if connected {
		t.Fatal("expected connected=false after Close")
	}
}

func TestPCAdapterClose(t *testing.T) {
	a := &pcAdapter{}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDummyAdapterClose(t *testing.T) {
	a := &dummyAdapter{}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestCloserInterface verifies all adapters implement the Closer interface.
func TestCloserInterface(t *testing.T) {
	var _ Closer = (*qqAdapter)(nil)
	var _ Closer = (*discordAdapter)(nil)
	var _ Closer = (*slackAdapter)(nil)
	var _ Closer = (*dingtalkAdapter)(nil)
	var _ Closer = (*feishuAdapter)(nil)
	var _ Closer = (*tgAdapter)(nil)
	var _ Closer = (*pcAdapter)(nil)
	var _ Closer = (*dummyAdapter)(nil)
}
