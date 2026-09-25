package im

import (
	"io"
	"net"
	"testing"
	"time"
)

// issue2747RecordingConn records SetWriteDeadline calls made through the
// net.Conn interface.
type issue2747RecordingConn struct {
	net.Conn
	deadlineSet     bool
	deadlineCleared bool
}

func (c *issue2747RecordingConn) SetWriteDeadline(t time.Time) error {
	if t.IsZero() {
		c.deadlineCleared = true
	} else {
		c.deadlineSet = true
	}
	return nil
}

// TestIssue2747SendRawSetsWriteDeadlineViaInterface pins the #2747 fix:
// sendRaw must set the write deadline through the net.Conn INTERFACE, not
// via a *net.TCPConn type assertion. The production conn is always *tls.Conn
// (defaultDialIRC wraps in tls.Client), so the old assertion never matched
// on any real connection and #2113's F2 anti-wedge protection was dead
// code. A non-*net.TCPConn conn (here: net.Pipe, same interface-only
// situation as *tls.Conn) reproduces the miss: the old code skipped the
// deadline entirely.
func TestIssue2747SendRawSetsWriteDeadlineViaInterface(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	// Drain the pipe so sendRaw's write completes.
	go func() {
		_, _ = io.Copy(io.Discard, server)
	}()

	rec := &issue2747RecordingConn{Conn: client}
	adapter := newTestTwitchAdapter(nil)
	adapter.mu.Lock()
	adapter.conn = rec
	adapter.mu.Unlock()

	if err := adapter.sendRaw("PING :probe"); err != nil {
		t.Fatalf("sendRaw: %v", err)
	}
	if !rec.deadlineSet {
		t.Fatalf("write deadline NOT set via net.Conn interface — #2747 regression: sendRaw still relies on a *net.TCPConn type assertion that never matches the production *tls.Conn")
	}
	if !rec.deadlineCleared {
		t.Fatalf("write deadline NOT cleared after write — later I/O on this conn would inherit the stale deadline")
	}
}
