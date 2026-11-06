//go:build !windows

package sessionidx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #5149 S1 unix-transport pins: the leader endpoint on darwin/linux.
// The socket file doubles as the leader "slot" - Listen fails while a
// live leader exists and reclaims the file when the previous leader
// died without unlinking it.

func newUnixTransport(t *testing.T) *UnixTransport {
	t.Helper()
	// t.TempDir() paths can exceed the 104-byte unix socket path limit
	// on darwin; use a short root instead.
	d, err := os.MkdirTemp("", "idx")
	if err != nil {
		t.Fatalf("short temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return &UnixTransport{path: filepath.Join(d, "idxd.sock")}
}

func TestUnixTransport_Roundtrip(t *testing.T) {
	tr := newUnixTransport(t)
	ln, err := tr.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	conn, err := tr.Dial(500 * time.Millisecond)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.Close()
}

func TestUnixTransport_SecondListenerRefusedWhileLeaderAlive(t *testing.T) {
	tr := newUnixTransport(t)
	ln, err := tr.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ln2, err := tr.Listen()
	if err == nil {
		ln2.Close()
		t.Fatal("second Listen must fail while the leader is alive")
	}
}

func TestUnixTransport_StaleSocketReclaimedAfterLeaderDeath(t *testing.T) {
	tr := newUnixTransport(t)
	// Simulate a crashed leader: a leftover socket file with nobody
	// listening behind it (Go's unix listener unlinks on Close, so we
	// plant the artifact directly rather than listen-then-close).
	if err := os.WriteFile(tr.path, nil, 0o600); err != nil {
		t.Fatalf("plant stale socket file: %v", err)
	}

	ln, err := tr.Listen()
	if err != nil {
		t.Fatalf("Listen must reclaim a stale socket: %v", err)
	}
	defer ln.Close()
	if c, derr := tr.Dial(500 * time.Millisecond); derr == nil {
		c.Close()
	} else {
		t.Fatalf("dial after reclaim: %v", derr)
	}
}

func TestUnixTransport_CleanupRespectsLiveLeader(t *testing.T) {
	tr := newUnixTransport(t)
	ln, err := tr.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	tr.Cleanup() // leader alive: must NOT remove the endpoint
	if c, derr := tr.Dial(500 * time.Millisecond); derr != nil {
		t.Fatalf("live leader lost its endpoint to Cleanup: %v", derr)
	} else {
		c.Close()
	}
}

// TestUnixTransport_CarriesHTTP pins plain HTTP over the unix leader
// transport (the loopback variant is pinned in transport_test.go,
// which runs on every platform).
func TestUnixTransport_CarriesHTTP(t *testing.T) {
	tr := newUnixTransport(t)
	ln, err := tr.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "pong")
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	client := &http.Client{
		Transport: &http.Transport{
			DialContext:       dialContextFor(tr, 500*time.Millisecond),
			DisableKeepAlives: true,
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("http://idxd.invalid/v1/ping")
	if err != nil {
		t.Fatalf("http get over unix transport: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Fatalf("unexpected body: %q", body)
	}
}
