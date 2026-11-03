//go:build linux

package tool

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

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

func TestIssue4546_BoundedRecoveryStillRuns(t *testing.T) {
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()

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
