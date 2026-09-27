package im

// Pin tests for the connectAndServe phase seams (r186). These lock the
// behavior-preserving extraction of dialIRC / markConnected / teardownConn /
// startKeepalive / readLoop / dispatchIRC / handleWelcome / handleNickInUse.
// Error prefixes ("proxy connect:", "tls handshake:", "connect:", "read:")
// are load-bearing: run() publishes them verbatim via publishState "error".
//
// net.Pipe is synchronous: every sendRaw write blocks until the server end
// is read. Readers are therefore started as goroutines BEFORE any call that
// can write (dispatchIRC / handleWelcome / handleNickInUse), mirroring how
// the real read loop and IRC server interleave.

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newIRCSeamAdapter wires an adapter whose a.conn is the client end of a
// net.Pipe; the returned server end stands in for the IRC server.
func newIRCSeamAdapter(t *testing.T) (*ircAdapter, net.Conn) {
	t.Helper()
	a := &ircAdapter{name: "seam-test", nick: "foo"}
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	a.mu.Lock()
	a.conn = client
	a.mu.Unlock()
	return a, server
}

// readIRCLinesAsync starts a reader goroutine that collects n CRLF-terminated
// lines. It MUST be started before any sendRaw-triggering call, otherwise the
// synchronous pipe deadlocks (writer blocks with no reader attached).
func readIRCLinesAsync(c net.Conn, n int) <-chan string {
	ch := make(chan string, n)
	go func() {
		defer close(ch)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		var buf []byte
		one := make([]byte, 1)
		for len(ch) < n {
			b, err := c.Read(one)
			if b > 0 {
				buf = append(buf, one[0])
				if one[0] == '\n' {
					ch <- strings.TrimRight(string(buf), "\r\n")
					buf = buf[:0]
				}
			}
			if err != nil {
				if len(buf) > 0 {
					ch <- strings.TrimRight(string(buf), "\r\n")
				}
				for len(ch) < n {
					ch <- "<read-error>"
				}
				return
			}
		}
	}()
	return ch
}

// awaitLine asserts the next line from ch equals want, with a hard deadline
// so a broken writer cannot hang the test.
func awaitLine(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("want %q, got %q", want, got)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}

func assertNoIRCWrite(t *testing.T, c net.Conn) {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	one := make([]byte, 1)
	if n, err := c.Read(one); err == nil {
		t.Fatalf("expected no write, got %q", string(one[:n]))
	}
}

func TestIRCDispatchIRC_PingRepliesPongAndBumpsPong(t *testing.T) {
	a, server := newIRCSeamAdapter(t)
	var lastPongNs atomic.Int64
	lastPongNs.Store(0)

	lines := readIRCLinesAsync(server, 1)
	a.dispatchIRC(context.Background(), &ircMessage{Command: "PING", Trailing: "srv1"}, &lastPongNs)

	awaitLine(t, lines, "PONG :srv1")
	if lastPongNs.Load() == 0 {
		t.Fatal("PING must update lastPongNs")
	}
}

func TestIRCDispatchIRC_PongUpdatesWithoutWrite(t *testing.T) {
	a, server := newIRCSeamAdapter(t)
	var lastPongNs atomic.Int64
	lastPongNs.Store(0)

	a.dispatchIRC(context.Background(), &ircMessage{Command: "PONG"}, &lastPongNs)

	if lastPongNs.Load() == 0 {
		t.Fatal("PONG must update lastPongNs")
	}
	assertNoIRCWrite(t, server)
}

func TestIRCDispatchIRC_UnknownCommandIsNoop(t *testing.T) {
	a, server := newIRCSeamAdapter(t)
	var lastPongNs atomic.Int64
	now := time.Now().UnixNano()
	lastPongNs.Store(now)

	a.dispatchIRC(context.Background(), &ircMessage{Command: "MODE"}, &lastPongNs)

	if lastPongNs.Load() != now {
		t.Fatal("unknown command must not touch lastPongNs")
	}
	assertNoIRCWrite(t, server)
}

func TestIRCHandleWelcome_NickServThenJoinsSkippingBlank(t *testing.T) {
	a, server := newIRCSeamAdapter(t)
	a.nickPass = "hunter2"
	a.channels = []string{" #a ", "", "#b"}

	lines := readIRCLinesAsync(server, 3)
	a.handleWelcome()

	want := []string{
		"PRIVMSG NickServ :IDENTIFY hunter2",
		"JOIN #a",
		"JOIN #b",
	}
	for _, w := range want {
		awaitLine(t, lines, w)
	}
	assertNoIRCWrite(t, server)
}

func TestIRCHandleWelcome_NoNickPassSkipsIdentify(t *testing.T) {
	a, server := newIRCSeamAdapter(t)
	a.nickPass = ""
	a.channels = []string{"#only"}

	lines := readIRCLinesAsync(server, 1)
	a.handleWelcome()

	awaitLine(t, lines, "JOIN #only")
	assertNoIRCWrite(t, server)
}

func TestIRCHandleNickInUse_SuffixesUnderscoreUnderLock(t *testing.T) {
	a, server := newIRCSeamAdapter(t)

	lines := readIRCLinesAsync(server, 1)
	a.handleNickInUse()

	if got := a.nick; got != "foo_" {
		t.Fatalf("nick want foo_, got %q", got)
	}
	awaitLine(t, lines, "NICK foo_")
}

func TestIRCDialIRC_PlainRefusedWrappedAsConnect(t *testing.T) {
	a := &ircAdapter{name: "dial-test", host: "127.0.0.1", port: 1, useTLS: false}

	conn, err := a.dialIRC("127.0.0.1:1")
	if err == nil {
		conn.Close()
		t.Fatal("want error dialing refused port")
	}
	if !strings.HasPrefix(err.Error(), "connect: ") {
		t.Fatalf("error must keep load-bearing `connect:` prefix, got %q", err.Error())
	}
}

func TestIRCDialIRC_TLSRefusedAlsoWrappedAsConnect(t *testing.T) {
	a := &ircAdapter{name: "dial-test", host: "127.0.0.1", port: 1, useTLS: true}

	conn, err := a.dialIRC("127.0.0.1:1")
	if err == nil {
		conn.Close()
		t.Fatal("want error dialing refused port")
	}
	if !strings.HasPrefix(err.Error(), "connect: ") {
		t.Fatalf("error must keep load-bearing `connect:` prefix, got %q", err.Error())
	}
}

func TestIRCDialIRC_ProxyRefusedWrappedAsProxyConnect(t *testing.T) {
	a := &ircAdapter{name: "dial-test", host: "irc.example.com", port: 6697, useTLS: true, proxy: "http://127.0.0.1:1"}

	conn, err := a.dialIRC("irc.example.com:6697")
	if err == nil {
		conn.Close()
		t.Fatal("want error dialing refused proxy")
	}
	if !strings.HasPrefix(err.Error(), "proxy connect: ") {
		t.Fatalf("error must keep load-bearing `proxy connect:` prefix, got %q", err.Error())
	}
}

func TestIRCReadLoop_CancelledContextSkipsDispatchAndReturnsNil(t *testing.T) {
	a, server := newIRCSeamAdapter(t)
	var lastPongNs atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- a.readLoop(ctx, a.conn, &lastPongNs) }()

	// One inbound line: ctx is checked before dispatch, so PING must NOT
	// produce a PONG write. Assert that before closing the server end
	// (SetReadDeadline fails on a closed pipe).
	if _, err := server.Write([]byte("PING :x\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	assertNoIRCWrite(t, server)
	server.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("cancelled ctx must return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readLoop did not return")
	}
}

func TestIRCReadLoop_CleanCloseReturnsNil(t *testing.T) {
	a, server := newIRCSeamAdapter(t)
	var lastPongNs atomic.Int64

	errCh := make(chan error, 1)
	go func() { errCh <- a.readLoop(context.Background(), a.conn, &lastPongNs) }()

	if _, err := server.Write([]byte("PING :x\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The readLoop goroutine is the writer here; drain its PONG from the
	// test goroutine so the synchronous pipe write completes.
	awaitLine(t, readIRCLinesAsync(server, 1), "PONG :x")
	server.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("clean close must return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readLoop did not return")
	}
}

func TestIRCReadLoop_OverlongLineWrapsScannerError(t *testing.T) {
	a, server := newIRCSeamAdapter(t)
	var lastPongNs atomic.Int64

	errCh := make(chan error, 1)
	go func() { errCh <- a.readLoop(context.Background(), a.conn, &lastPongNs) }()

	// >512KiB without a newline overflows the scanner buffer. Use a write
	// deadline so the writer gives up once the scanner stops reading.
	if err := server.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	big := strings.Repeat("a", 600*1024)
	go func() { _, _ = server.Write([]byte(big)) }()

	select {
	case err := <-errCh:
		if err == nil || !strings.HasPrefix(err.Error(), "read: ") {
			t.Fatalf("scanner error must keep load-bearing `read:` prefix, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("readLoop did not return on overlong line")
	}
}
