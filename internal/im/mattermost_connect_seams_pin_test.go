package im

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Pin tests for mattermostAdapter.connectAndServe phase behavior.
// Golden-filter discipline (r210): these tests were written against the
// UNREFACTORED single-body implementation and must stay green across the
// behavior-preserving seam extraction (authREST / dialWebsocket / authWS /
// startHeartbeat / readWSEvents) without modification.

func newPinMattermostAdapter(srv *httptest.Server) *mattermostAdapter {
	return &mattermostAdapter{
		name:      "pin",
		baseURL:   srv.URL,
		token:     "tok",
		conn:      srv.Client(),
		seen:      make(map[string]time.Time),
		replyMode: "off",
	}
}

// waitForConnectError runs connectAndServe with a hard watchdog so a
// regression can never hang the suite.
func waitForConnectError(t *testing.T, a *mattermostAdapter, ctx context.Context) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() { errCh <- a.connectAndServe(ctx) }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("connectAndServe did not return within watchdog")
		return nil
	}
}

// Gate order: REST auth failure must surface as "auth:" - proving the auth
// phase runs and returns before any WebSocket dial attempt.
func TestPinConnectAndServeAuthPhaseBeforeDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/users/me") {
			http.Error(w, `{"message":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	a := newPinMattermostAdapter(srv)
	err := waitForConnectError(t, a, context.Background())
	if err == nil {
		t.Fatal("expected auth error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "auth:") {
		t.Fatalf("expected error prefix %q, got %q", "auth:", err.Error())
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.connected {
		t.Fatal("must not be connected after auth failure")
	}
	if a.ws != nil {
		t.Fatal("ws must stay nil after auth failure")
	}
}

// After successful REST auth, a failed WebSocket dial surfaces as "ws dial:"
// (upgrade rejected). Bot identity is already persisted at that point.
func TestPinConnectAndServeWSDialFailureAfterAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/users/me"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"bot1","username":"pinbot"}`))
		default:
			// WebSocket endpoint refuses the upgrade.
			http.Error(w, "no upgrade", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	a := newPinMattermostAdapter(srv)
	err := waitForConnectError(t, a, context.Background())
	if err == nil {
		t.Fatal("expected ws dial error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "ws dial:") {
		t.Fatalf("expected error prefix %q, got %q", "ws dial:", err.Error())
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.botUserID != "bot1" || a.botUsername != "pinbot" {
		t.Fatalf("bot identity not persisted before dial: id=%q user=%q", a.botUserID, a.botUsername)
	}
}

// Successful auth handshake followed by server-side close: the read loop must
// exit with a wrapped "ws read:" error and the cleanup path must reset the
// connection state (connected=false, ws=nil).
func TestPinConnectAndServeReadLoopCloseAndCleanup(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/users/me"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"bot2","username":"pinbot2"}`))
		case strings.HasSuffix(r.URL.Path, "/websocket"):
			c, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			// Consume the auth challenge frame, then drop the connection so
			// the client read loop terminates with a read error.
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
			_ = c.Close()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := newPinMattermostAdapter(srv)
	err := waitForConnectError(t, a, context.Background())
	if err == nil {
		t.Fatal("expected read-loop error after server close, got nil")
	}
	if !strings.HasPrefix(err.Error(), "ws read:") {
		t.Fatalf("expected error prefix %q, got %q", "ws read:", err.Error())
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.connected {
		t.Fatal("cleanup must reset connected=false")
	}
	if a.ws != nil {
		t.Fatal("cleanup must clear a.ws")
	}
}
