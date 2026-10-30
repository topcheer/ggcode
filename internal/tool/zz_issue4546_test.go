//go:build linux

package tool

// #4546 companion tests - desktop_control_linux exec lifecycle:
//   A) runAppResult is fire-and-forget: the caller's ctx must NOT gate the
//      child's lifetime (parallel tool calls run under a 30s WithTimeout, so
//      exec.CommandContext SIGKILLed launched GUI apps 30s after the tool
//      already returned OK).
//   B) the hold_key best-effort recovery sequences (Wayland runArgvSeq and
//      holdKeyX11's xdotool keyup) must be bounded - a half-dead daemon used
//      to hang the synchronous Execute path forever.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// A (runtime): launching with an ALREADY-CANCELLED context must succeed.
// Under the old CommandContext this failed at Start ("context canceled") -
// and with a 30s parallel-tool timeout the app was killed mid-run.
func TestIssue4546_RunAppSurvivesCancelledContext(t *testing.T) {
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("/bin/sleep not present")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := runAppResult(ctx, "/bin/sleep", "1")
	if err != nil {
		t.Fatalf("#4546: fire-and-forget launch must not fail on a cancelled ctx: %v", err)
	}
	if !strings.Contains(res.Content, "OK: launched") {
		t.Fatalf("#4546: unexpected result: %q", res.Content)
	}
}

// A/B (source pins): the fire-and-forget launcher must not use
// CommandContext, and both recovery sites must be bounded by a timeout.
func TestIssue4546_SourcePins(t *testing.T) {
	src, err := os.ReadFile("desktop_control_linux.go")
	if err != nil {
		t.Skip("source not readable in this environment")
	}
	s := string(src)

	runAppStart := strings.Index(s, "func runAppResult(")
	runAppEnd := strings.Index(s[runAppStart:], "\nfunc ")
	if runAppStart < 0 || runAppEnd < 0 {
		t.Fatal("runAppResult not found")
	}
	body := s[runAppStart : runAppStart+runAppEnd]
	if strings.Contains(body, "exec.CommandContext(") {
		t.Fatal("#4546: runAppResult must use exec.Command - CommandContext kills the fire-and-forget child when the caller ctx fires")
	}
	if !strings.Contains(body, "cmd.Wait()") {
		t.Fatal("#4546: runAppResult must keep the background Wait (zombie reaping)")
	}

	// B: no unbounded context.Background() may be passed directly into a
	// recovery exec/seq - both sites must go through a bounded rctx.
	for _, unbounded := range []string{
		"runArgvSeq(context.Background(),",
		"exec.CommandContext(context.Background(),",
	} {
		if strings.Contains(s, unbounded) {
			t.Fatalf("#4546: unbounded recovery call remains: %q - best-effort release must be bounded (WithTimeout)", unbounded)
		}
	}
	if got := strings.Count(s, "context.WithTimeout(context.Background(), 5*time.Second)"); got < 2 {
		t.Fatalf("#4546: expected bounded recovery at Wayland + X11 sites, found %d", got)
	}
}

// B (semantics): the release recovery itself still works within its window
// (proves the bound did not break the happy path).
func TestIssue4546_BoundedRecoveryStillRuns(t *testing.T) {
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	// "true" exits 0 immediately: a successful release sequence under a
	// bounded context must complete well inside the deadline.
	done := make(chan error, 1)
	go func() { done <- runArgvSeq(rctx, [][]string{{"true"}}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("#4546: bounded recovery broke the happy path: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("#4546: bounded recovery hung")
	}
}
