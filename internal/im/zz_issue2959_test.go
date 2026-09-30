package im

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// zz_issue2959_test.go - regression probes for #2959: POST /shutdown used to
// be a no-op - it verified the bearer token, replied "shutting_down", and
// touched nothing (no cancel, no Close). The shutdown-token chain (generate
// -> portFile line 2 -> eval stop_daemon -> POST) was dead code and only the
// caller's unconditional SIGTERM actually stopped the daemon. The endpoint
// must now cancel the context start() derived, which fires the graceful
// srv.Close+listener.Close stop goroutine.

func TestIssue2959ShutdownEndpointCancelsContext(t *testing.T) {
	s := newHTTPServer(&dummyAdapter{})
	portFile := filepath.Join(t.TempDir(), "port")
	ctx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	s.start(ctx, "127.0.0.1:0", portFile)

	raw, err := os.ReadFile(portFile)
	if err != nil || len(strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)) != 2 {
		t.Fatalf("#2959: port file must carry addr+token, got err=%v content=%q", err, string(raw))
	}
	parts := strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)
	addr, token := parts[0], strings.TrimSpace(parts[1])

	client := util.NewInsecureAwareClient(5 * time.Second)
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/shutdown", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("#2959: POST /shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("#2959: status = %d, want 200", resp.StatusCode)
	}

	// The graceful path must fire: derived ctx cancels the parent's stop
	// goroutine closes the listener. Assert via ctx.Done() (start wired the
	// derived ctx into the parent chain: cancel propagates upward? No - it
	// cancels the CHILD. Assert the listener closed instead: a follow-up
	// request must fail once the stop goroutine ran.)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		probe, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/shutdown", nil)
		probe.Header.Set("Authorization", "Bearer "+token)
		if _, err := client.Do(probe); err != nil {
			return // server closed - graceful stop ran
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("#2959: listener still serving 3s after shutdown POST - cancel was a no-op")
}

func TestIssue2959WrongTokenDoesNotStop(t *testing.T) {
	s := newHTTPServer(&dummyAdapter{})
	portFile := filepath.Join(t.TempDir(), "port")
	ctx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	s.start(ctx, "127.0.0.1:0", portFile)

	raw, _ := os.ReadFile(portFile)
	parts := strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)
	addr := parts[0]

	client := util.NewInsecureAwareClient(5 * time.Second)
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/shutdown", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("#2959: probe POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("#2959: wrong token status = %d, want 401", resp.StatusCode)
	}
	// Server must still be serving: same endpoint reachable again.
	probe, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/shutdown", nil)
	probe.Header.Set("Authorization", "Bearer wrong-token")
	if resp2, err := client.Do(probe); err != nil || resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("#2959: server must keep serving after a rejected shutdown (err=%v)", err)
	} else {
		resp2.Body.Close()
	}
}
