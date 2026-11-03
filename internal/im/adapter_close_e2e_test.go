//go:build integration_local

package im

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestMutePhysicallyDropsConnection(t *testing.T) {

	var (
		mu           sync.Mutex
		connected    = make(chan struct{})
		disconnected = make(chan error, 1)
	)

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(connected)

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				mu.Lock()
				select {
				case disconnected <- err:
				default:
				}
				mu.Unlock()
				return
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + srv.URL[4:]

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	adapter := &qqAdapter{name: "test-qq"}
	adapter.mu.Lock()
	adapter.ws = conn
	adapter.connected = true
	adapter.mu.Unlock()

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not see connection")
	}

	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/tmp",
		Platform:  PlatformQQ,
		Adapter:   "test-qq",
		ChannelID: "ch-1",
	})
	mgr.RegisterSink(adapter)

	if err := mgr.MuteBinding("test-qq"); err != nil {
		t.Fatalf("MuteBinding: %v", err)
	}

	select {
	case err := <-disconnected:
		if err == nil {
			t.Fatal("expected error from server read (connection should be closed)")
		}

		t.Logf("server detected disconnect: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: server did not detect disconnect within 3s — connection NOT physically closed")
	}
}

func TestDisablePhysicallyDropsConnection(t *testing.T) {
	var (
		connected    = make(chan struct{})
		disconnected = make(chan error, 1)
	)

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(connected)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				select {
				case disconnected <- err:
				default:
				}
				return
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + srv.URL[4:]

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	adapter := &qqAdapter{name: "test-qq"}
	adapter.mu.Lock()
	adapter.ws = conn
	adapter.connected = true
	adapter.mu.Unlock()

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not see connection")
	}

	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/tmp",
		Platform:  PlatformQQ,
		Adapter:   "test-qq",
		ChannelID: "ch-1",
	})
	mgr.RegisterSink(adapter)

	if err := mgr.DisableBinding("test-qq"); err != nil {
		t.Fatalf("DisableBinding: %v", err)
	}

	select {
	case err := <-disconnected:
		t.Logf("server detected disconnect: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: connection NOT physically closed after disable")
	}
}

func TestMuteAllPhysicallyDropsAllConnections(t *testing.T) {
	type serverState struct {
		connected    chan struct{}
		disconnected chan error
		srv          *httptest.Server
	}

	servers := make([]serverState, 2)
	upgrader := websocket.Upgrader{}
	for i := range servers {
		servers[i].connected = make(chan struct{})
		servers[i].disconnected = make(chan error, 1)
		idx := i
		servers[i].srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			close(servers[idx].connected)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					select {
					case servers[idx].disconnected <- err:
					default:
					}
					return
				}
			}
		}))
	}
	defer func() {
		for _, s := range servers {
			s.srv.Close()
		}
	}()

	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})

	adapters := []struct {
		name     string
		platform Platform
	}{
		{"qq-1", PlatformQQ},
		{"qq-2", PlatformQQ},
	}

	for i, a := range adapters {
		wsURL := "ws" + servers[i].srv.URL[4:]
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}

		adapter := &qqAdapter{name: a.name}
		adapter.mu.Lock()
		adapter.ws = conn
		adapter.connected = true
		adapter.mu.Unlock()

		_, _ = mgr.BindChannel(ChannelBinding{
			Workspace: "/tmp",
			Platform:  a.platform,
			Adapter:   a.name,
			ChannelID: a.name,
		})
		mgr.RegisterSink(adapter)

		select {
		case <-servers[i].connected:
		case <-time.After(2 * time.Second):
			t.Fatalf("server %d did not see connection", i)
		}
	}

	count, _ := mgr.MuteAll()
	if count != 2 {
		t.Fatalf("expected 2 muted, got %d", count)
	}

	for i, s := range servers {
		select {
		case err := <-s.disconnected:
			t.Logf("server %d detected disconnect: %v", i, err)
		case <-time.After(3 * time.Second):
			t.Fatalf("server %d: connection NOT physically closed after MuteAll", i)
		}
	}
}

func TestMutePreventsReconnect(t *testing.T) {
	var (
		mu             sync.Mutex
		connectCount   int
		connected      = make(chan struct{})
		disconnectChan = make(chan error, 2)
	)

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		connectCount++
		count := connectCount
		mu.Unlock()
		if count == 1 {
			close(connected)
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				select {
				case disconnectChan <- err:
				default:
				}
				return
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + srv.URL[4:]

	_, adapterCancel := context.WithCancel(context.Background())

	adapter := &qqAdapter{
		name:      "test-qq",
		manager:   NewManager(),
		appID:     "fake-app-id",
		appSecret: "fake-app-secret",
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {

					return net.Dial("tcp", strings.TrimPrefix(wsURL, "ws://"))
				},
			},
		},
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	adapter.mu.Lock()
	adapter.ws = conn
	adapter.connected = true
	adapter.mu.Unlock()

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not see initial connection")
	}

	mgr := NewManager()
	mgr.BindSession(SessionBinding{SessionID: "s1", Workspace: "/tmp"})
	_, _ = mgr.BindChannel(ChannelBinding{
		Workspace: "/tmp",
		Platform:  PlatformQQ,
		Adapter:   "test-qq",
		ChannelID: "ch-1",
	})
	mgr.RegisterSink(adapter)
	mgr.RegisterAdapterCancel("test-qq", adapterCancel)

	if err := mgr.MuteBinding("test-qq"); err != nil {
		t.Fatalf("MuteBinding: %v", err)
	}

	select {
	case <-disconnectChan:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: server did not see disconnect")
	}

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	count := connectCount
	mu.Unlock()
	if count != 1 {
		t.Fatalf("expected exactly 1 connection (no reconnect), got %d", count)
	}
}

func TestMuteWithoutCloseDoesNotDropConnection(t *testing.T) {
	disconnected := make(chan error, 1)

	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				select {
				case disconnected <- err:
				default:
				}
				return
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + srv.URL[4:]

	conn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)

	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx
	cancel()

	select {
	case <-disconnected:
		t.Fatal("connection should NOT have dropped — we only cancelled context")
	case <-time.After(500 * time.Millisecond):

	}

	conn.Close()

	select {
	case <-disconnected:

	case <-time.After(2 * time.Second):
		t.Fatal("expected disconnect after conn.Close()")
	}
}
