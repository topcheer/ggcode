package sessionidx

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Transport is the IPC surface for the shared-index leader service
// (#5149). Exactly one ggcode instance per machine is the leader: the
// first instance that successfully Listen()s hosts the shared index
// in-process (single-binary constraint - no separate daemon process);
// every other instance is a client and Dial()s it. When the leader
// exits, its listener closes and the next client that fails a probe
// retries Listen() to take over.
//
// Platform split (QA correction B, #5149 design review): unix domain
// socket on darwin/linux; on Windows a 127.0.0.1-bound loopback
// listener with an ephemeral port published in a discovery file. A
// loopback bind does not trigger the Windows firewall prompt and the
// ephemeral port avoids collisions, so no named pipe (non-stdlib) and
// no fixed port are needed.
type Transport interface {
	// Name identifies the transport for logs ("unix", "loopback").
	Name() string
	// Listen binds the leader endpoint. It MUST fail while a live
	// leader holds it, and MUST reclaim the endpoint when a previous
	// leader died without cleanup (stale socket file / discovery file).
	Listen() (net.Listener, error)
	// Dial connects to the leader (if any) within the timeout.
	Dial(timeout time.Duration) (net.Conn, error)
	// Cleanup removes any endpoint artifacts the previous leader left
	// behind. Safe to call when nothing is bound.
	Cleanup()
	// Descriptor returns a dial descriptor for the bound endpoint in the
	// legacy "network:address" form ("unix:/path" or
	// "tcp:127.0.0.1:port"), for the sessionidxd.addr rolling-upgrade
	// file. Empty when not currently listening.
	Descriptor() string
}

// ErrLeaderAlive is returned by Listen when another live instance
// already holds the endpoint: the caller should become a client.
var ErrLeaderAlive = fmt.Errorf("sessionidx: leader already alive")

// discovery is the content of the Windows discovery file: the
// loopback port of the current leader plus identity fields for
// debugging. Port is the only field dialers rely on.
type discovery struct {
	Port    int   `json:"port"`
	PID     int   `json:"pid"`
	Started int64 `json:"started_unix_nano"`
}

func writeDiscovery(path string, d discovery) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := fmt.Appendf(nil, "%d\n%d\n%d\n", d.Port, d.PID, d.Started)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic publish
}

func readDiscovery(path string) (discovery, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return discovery{}, err
	}
	var d discovery
	if _, err := fmt.Sscanf(string(body), "%d\n%d\n%d\n", &d.Port, &d.PID, &d.Started); err != nil {
		return discovery{}, fmt.Errorf("sessionidx: parse discovery %s: %w", path, err)
	}
	if d.Port <= 0 || d.Port > 65535 {
		return discovery{}, fmt.Errorf("sessionidx: discovery %s has invalid port %d", path, d.Port)
	}
	return d, nil
}
