package sessionidx

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// #5149 S1 loopback-transport pins (the Windows leader endpoint). The
// whole LoopbackTransport is platform-neutral net code, so these run on
// every platform - including the Windows host where real-world
// validation happens. Unix-socket pins live in
// transport_unix_test.go behind a !windows build tag.

func newLoopbackTransport(t *testing.T) *LoopbackTransport {
	t.Helper()
	return &LoopbackTransport{discPath: filepath.Join(t.TempDir(), "idxd.discovery")}
}

func TestLoopbackTransport_RoundtripAndDiscoveryFile(t *testing.T) {
	tr := newLoopbackTransport(t)
	ln, err := tr.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	d, err := readDiscovery(tr.discPath)
	if err != nil {
		t.Fatalf("read discovery: %v", err)
	}
	if d.Port <= 0 {
		t.Fatalf("discovery port invalid: %d", d.Port)
	}

	conn, err := tr.Dial(500 * time.Millisecond)
	if err != nil {
		t.Fatalf("dial via discovery port: %v", err)
	}
	conn.Close()
}

func TestLoopbackTransport_DialFailsWithoutDiscovery(t *testing.T) {
	tr := newLoopbackTransport(t)
	if _, err := tr.Dial(200 * time.Millisecond); err == nil {
		t.Fatal("dial must fail when no leader ever published a discovery file")
	}
}

func TestLoopbackTransport_DialFailsOnStaleDiscovery(t *testing.T) {
	tr := newLoopbackTransport(t)
	// Publish a port nobody serves: bind, capture, close, keep file.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listener: %v", err)
	}
	port := dead.Addr().(*net.TCPAddr).Port
	dead.Close()
	if werr := writeDiscovery(tr.discPath, discovery{Port: port, PID: -1}); werr != nil {
		t.Fatalf("write stale discovery: %v", werr)
	}
	if _, derr := tr.Dial(300 * time.Millisecond); derr == nil {
		t.Fatal("dial must fail against a stale discovery port")
	}

	// A fresh leader must take over regardless: new ephemeral port,
	// discovery overwritten atomically.
	ln, lerr := tr.Listen()
	if lerr != nil {
		t.Fatalf("listen over stale discovery: %v", lerr)
	}
	defer ln.Close()
	if c, derr := tr.Dial(500 * time.Millisecond); derr != nil {
		t.Fatalf("dial after takeover: %v", derr)
	} else {
		c.Close()
	}
}

// TestLoopbackTransport_CarriesHTTP pins the protocol claim of the
// #5149 design: plain HTTP/1.1 over the leader transport works with
// the stdlib server and client, so the shared-index service needs no
// custom wire format.
func TestLoopbackTransport_CarriesHTTP(t *testing.T) {
	tr := newLoopbackTransport(t)
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
	// http:// scheme with a dummy host: the custom DialContext ignores
	// the address and routes over the leader transport (loopback), so
	// TLS is not applicable here.
	resp, err := client.Get("http://idxd.invalid/v1/ping")
	if err != nil {
		t.Fatalf("http get over %s transport: %v", tr.Name(), err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Fatalf("unexpected body: %q", body)
	}
}

// dialContextFor adapts a Transport to net/http's DialContext so the
// standard HTTP client can talk over the leader transport without
// knowing its address scheme.
func dialContextFor(tr Transport, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	return func(context.Context, string, string) (net.Conn, error) {
		return tr.Dial(timeout)
	}
}
