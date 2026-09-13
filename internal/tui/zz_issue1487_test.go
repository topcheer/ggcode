package tui

// #1487 case E regression: the session-created callback used to fire
// ONCE and then be nilled, so after --resume A a mid-run /clear to B
// never rewrote the port file - it kept pointing at stale A for the
// rest of the run (WebUI/mobile attach landed on the wrong session)
// while the exit defer deleted A's file. The callback now fires on
// every session switch; onSessionCreated rewrites the port file and
// re-points the cleanup key.

import (
	"os"
	"testing"

	"github.com/topcheer/ggcode/internal/runfile"
	"github.com/topcheer/ggcode/internal/session"
)

func new1487REPL(t *testing.T) *REPL {
	t.Helper()
	dir := t.TempDir()
	if err := os.Setenv("GGCODE_HOME", dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Unsetenv("GGCODE_HOME") })
	return &REPL{
		webuiAddr:    "127.0.0.1:1",
		webuiToken:   "tok",
		workingDir:   dir,
		portFileMode: "supervised",
	}
}

// The core switch case: initial=A, session created B -> port file must
// re-point to B and the cleanup key must follow (old code returned
// early here, freezing the port file at A forever).
func TestIssue1487E_SwitchRewritesPortFile(t *testing.T) {
	r := new1487REPL(t)
	r.SetInitialSessionID("sess-AAAA", "supervised")
	r.onSessionCreated("sess-BBBB")

	pf, err := runfile.Read("sess-BBBB")
	if err != nil || pf == nil {
		t.Fatalf("port file for sess-BBBB must exist after switch, got %v", err)
	}
	if pf.SessionID != "sess-BBBB" {
		t.Fatalf("port file must point at sess-BBBB, got %q", pf.SessionID)
	}
	if got := r.CurrentSessionID(); got != "sess-BBBB" {
		t.Fatalf("cleanup key must follow the switch, got %q", got)
	}
}

// Same-ID re-fire stays idempotent (cleanup key unchanged).
func TestIssue1487E_SameIDIdempotent(t *testing.T) {
	r := new1487REPL(t)
	r.SetInitialSessionID("sess-AAAA", "supervised")
	r.onSessionCreated("sess-AAAA")
	if got := r.CurrentSessionID(); got != "sess-AAAA" {
		t.Fatalf("same-ID fire must keep cleanup key, got %q", got)
	}
}

// The model-side half: the callback must NOT be nilled after firing
// (old code froze the port file at the first session).
func TestIssue1487E_CallbackNotNilledAfterFire(t *testing.T) {
	m := &Model{}
	fired := 0
	m.SetSessionCreatedCallback(func(id string) { fired++ })
	m.SetSession(&session.Session{ID: "sess-one"}, nil)
	m.SetSession(&session.Session{ID: "sess-two"}, nil)
	if fired != 2 {
		t.Fatalf("callback must fire on every session, fired %d", fired)
	}
}
