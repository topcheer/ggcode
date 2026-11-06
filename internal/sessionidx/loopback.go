package sessionidx

import (
	"fmt"
	"net"
	"os"
	"time"
)

// LoopbackTransport is the leader endpoint used on Windows (and
// testable everywhere): a 127.0.0.1-bound TCP listener with an
// ephemeral port (:0), published via a discovery file. QA correction B
// (#5149): loopback binds do not trigger the Windows firewall prompt
// and the ephemeral port removes fixed-port collisions, replacing the
// non-stdlib named pipe.
//
// The whole implementation is platform-neutral net code, so it is
// fully testable on darwin/linux; only the NewTransport dispatch is
// Windows-specific.
type LoopbackTransport struct {
	discPath string
	lastPort int // set by Listen; 0 when not bound
}

func (t *LoopbackTransport) Name() string { return "loopback" }

func (t *LoopbackTransport) Listen() (net.Listener, error) {
	// Stale discovery from a dead leader never blocks us: we always
	// bind a fresh ephemeral port and atomically overwrite the file.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close()
		return nil, fmt.Errorf("sessionidx: loopback listener addr %v is not TCP", ln.Addr())
	}
	d := discovery{Port: tcp.Port, PID: os.Getpid(), Started: time.Now().UnixNano()}
	if werr := writeDiscovery(t.discPath, d); werr != nil {
		ln.Close()
		return nil, fmt.Errorf("sessionidx: publish discovery %s: %w", t.discPath, werr)
	}
	t.lastPort = tcp.Port
	return ln, nil
}

func (t *LoopbackTransport) Dial(timeout time.Duration) (net.Conn, error) {
	d, err := readDiscovery(t.discPath)
	if err != nil {
		return nil, err
	}
	return net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", d.Port), timeout)
}

func (t *LoopbackTransport) Cleanup() {
	// Only remove the discovery file when nobody answers it; a live
	// leader must keep its published port.
	if c, derr := t.Dial(200 * time.Millisecond); derr == nil {
		c.Close()
		return
	}
	_ = os.Remove(t.discPath)
}

func (t *LoopbackTransport) Descriptor() string {
	if t.lastPort <= 0 {
		return ""
	}
	return fmt.Sprintf("tcp:127.0.0.1:%d", t.lastPort)
}
