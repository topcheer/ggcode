//go:build !windows

package sessionidx

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// UnixTransport is the leader endpoint on darwin/linux: one unix
// domain socket file. The socket file doubles as the leader "slot" -
// Listen fails while a live leader exists and reclaims the file when
// the previous leader died without unlinking it.
type UnixTransport struct {
	path string
}

// NewTransport builds the platform-default leader transport rooted at
// baseDir (typically the ggcode config dir). On non-Windows platforms
// this is a unix domain socket at baseDir/idxd.sock.
func NewTransport(baseDir string) Transport {
	return &UnixTransport{path: filepath.Join(baseDir, "idxd.sock")}
}

func (t *UnixTransport) Name() string { return "unix" }

func (t *UnixTransport) Listen() (net.Listener, error) {
	ln, err := net.Listen("unix", t.path)
	if err == nil {
		return ln, nil
	}
	// Endpoint busy: distinguish a live leader from a stale socket file
	// left by a crashed previous leader.
	if c, derr := net.DialTimeout("unix", t.path, 200*time.Millisecond); derr == nil {
		c.Close()
		return nil, fmt.Errorf("%w: %s", ErrLeaderAlive, t.path)
	}
	// Stale: nobody answers, reclaim and retry once.
	if rmErr := os.Remove(t.path); rmErr != nil {
		return nil, err
	}
	ln2, err2 := net.Listen("unix", t.path)
	if err2 != nil {
		return nil, err2
	}
	return ln2, nil
}

func (t *UnixTransport) Dial(timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", t.path, timeout)
}

func (t *UnixTransport) Cleanup() {
	// Only remove a socket nobody is answering; a live leader must
	// never have its endpoint pulled out from under it.
	if c, derr := net.DialTimeout("unix", t.path, 200*time.Millisecond); derr == nil {
		c.Close()
		return
	}
	_ = os.Remove(t.path)
}

func (t *UnixTransport) Descriptor() string {
	// A live leader keeps its endpoint; a stale file answers nothing.
	if c, derr := net.DialTimeout("unix", t.path, 200*time.Millisecond); derr == nil {
		c.Close()
		return "unix:" + t.path
	}
	return ""
}
