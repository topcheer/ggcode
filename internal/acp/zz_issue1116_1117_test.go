package acp

// Regression tests for GitHub issues #1116 and #1117 (internal/acp).
//
// #1116 - setupAskUserHandler overwrote a single SetHandler slot on the
// shared ask_user tool singleton, so with >=2 concurrent ACP sessions the
// last-created loop captured every session's ask_user traffic (same
// last-writer-wins class as #1047). Fix: a registry-level dispatcher keyed by
// session ID; each AgentLoop registers its own handler.
//
// #1117 - Start() installed readCtx/cancelRead but readLoop's EOF/read-error
// returns never cancelled the context. A later Start() overwrote c.cancelRead
// and the stale generation leaked together with async handleAgentRequest
// goroutines spawned on it. Fix: spawnReadLoop defers cancelRead on any exit,
// and Start() defensively cancels the previous generation before overwrite.

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tool"
)

// newIssue1116Fixture builds ONE shared registry holding the ask_user
// singleton plus a loop per session, each wired to its own transport whose
// outbound bytes land in the returned buffers. Mirrors the real #1116 shape:
// concurrent ACP sessions share a single tool registry.
func newIssue1116Fixture(t *testing.T, sessionIDs ...string) ([]*AgentLoop, []*bytes.Buffer) {
	t.Helper()

	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewAskUserTool()); err != nil {
		t.Fatalf("register ask_user singleton: %v", err)
	}
	var loops []*AgentLoop
	var bufs []*bytes.Buffer
	for _, id := range sessionIDs {
		out := &bytes.Buffer{}
		transport := NewTransport(strings.NewReader(""), out)
		session := &Session{ID: id, CWD: t.TempDir()}
		loops = append(loops, NewAgentLoop(&config.Config{}, registry, transport, session, ClientCapabilities{}, nil))
		bufs = append(bufs, out)
	}
	return loops, bufs
}

func issue1116Ctx(sessionID string) (context.Context, context.CancelFunc) {
	return context.WithTimeout(
		context.WithValue(context.Background(), acpAskUserSessionKey{}, sessionID),
		400*time.Millisecond,
	)
}

// TestIssue1116_PerSessionAskUserHandlerRouting verifies that handlers set by
// earlier loops remain reachable after later loops are created, and that each
// ask_user call is routed to the session named on the run context (the old
// code let the last-created loop steal all traffic).
func TestIssue1116_PerSessionAskUserHandlerRouting(t *testing.T) {
	loops, bufs := newIssue1116Fixture(t, "sess-A", "sess-B", "sess-C")
	loopA, loopB, loopC := loops[0], loops[1], loops[2]
	bufA, bufB := bufs[0], bufs[1]

	entry, ok := loopA.registry.Get("ask_user")
	if !ok {
		t.Fatal("ask_user missing from shared registry after setupAskUserHandler")
	}
	disp, ok := entry.(*acpAskUserSession)
	if !ok {
		t.Fatalf("expected *acpAskUserSession dispatcher in registry, got %T", entry)
	}

	routeAndExpect := func(sessionID string, wantBuf *bytes.Buffer) {
		ctx, cancel := issue1116Ctx(sessionID)
		defer cancel()
		res, err := disp.Execute(ctx, []byte(`{}`))
		if err != nil {
			t.Fatalf("dispatcher Execute(%s): %v", sessionID, err)
		}
		// The per-loop handler forwards to RequestPermission on the owning
		// loop's transport; our short ctx makes the wait fail, so the routed
		// call shows up as an error result mentioning the permission request.
		if !res.IsError {
			t.Fatalf("Execute(%s): expected error result from aborted approval wait", sessionID)
		}
		if !strings.Contains(res.Content, "ask_user failed") {
			t.Fatalf("Execute(%s): unexpected content %q", sessionID, res.Content)
		}
		if !bytes.Contains(wantBuf.Bytes(), []byte(`session/request_permission`)) {
			t.Fatalf("session %s: its transport never received the permission request", sessionID)
		}
	}

	// Routing works even for loops created before other sessions constructed
	// their own loops - this is exactly what last-writer-wins broke (#1116).
	routeAndExpect("sess-A", bufA)
	routeAndExpect("sess-B", bufB)
	if loopB.registry != loopA.registry {
		t.Fatal("loops A and B unexpectedly use different registries")
	}
	_ = loopC // third construction must not have displaced A/B handlers

	// The base singleton's SetHandler slot must remain untouched so no stale
	// global handler can answer for sessions it does not belong to.
}

// TestIssue1116_UnknownTaggedSessionGetsCleanError verifies that a request
// tagged with a session ID that has no registered handler fails cleanly
// instead of falling through to some other session's legacy default (#1116).
func TestIssue1116_UnknownTaggedSessionGetsCleanError(t *testing.T) {
	loops, _ := newIssue1116Fixture(t, "sess-A")

	entry, _ := loops[0].registry.Get("ask_user")
	disp, ok := entry.(*acpAskUserSession)
	if !ok {
		t.Fatalf("expected *acpAskUserSession dispatcher, got %T", entry)
	}

	ctx, cancel := issue1116Ctx("sess-not-registered")
	defer cancel()
	res, err := disp.Execute(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError result for unknown tagged session")
	}
	if !strings.Contains(res.Content, "no handler") {
		t.Fatalf("unexpected content: %q", res.Content)
	}
}

// TestIssue1116_LegacyDefaultPreservedButRegistryReplaced checks both sides
// of the compatibility contract: the registry slot becomes the dispatcher,
// while the raw singleton retains its legacy default handler so pre-existing
// untagged callers keep functioning (#1116).
func TestIssue1116_LegacyDefaultPreservedButRegistryReplaced(t *testing.T) {
	registry := tool.NewRegistry()
	base := tool.NewAskUserTool()
	if err := registry.Register(base); err != nil {
		t.Fatalf("register singleton: %v", err)
	}

	sessions := []string{"s1", "s2", "s3"}
	for _, id := range sessions {
		transport := NewTransport(strings.NewReader(""), io.Discard)
		session := &Session{ID: id, CWD: t.TempDir()}
		NewAgentLoop(&config.Config{}, registry, transport, session, ClientCapabilities{}, nil)
	}

	dispatcher, _ := registry.Get("ask_user")
	if _, ok := dispatcher.(*acpAskUserSession); !ok {
		t.Fatalf("registry serves %T instead of dispatcher", dispatcher)
	}
	if !base.HasHandler() {
		t.Fatal("legacy default handler was dropped from the singleton")
	}
}

// TestIssue1117_ReadLoopEOFCancelsStaleGeneration drives a real EOF return
// out of readLoop and asserts that the generation's read context is cancelled
// even though Stop()/Close() were never called (the #1117 leak).
func TestIssue1117_ReadLoopEOFCancelsStaleGeneration(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	trReader := strings.NewReader("") // no input needed; EOF comes from pipe close
	c := &Client{
		def:       DiscoveredAgent{Def: AgentDef{Name: "issue1117-agent"}},
		done:      make(chan struct{}),
		transport: NewTransport(pr, io.Discard),
	}
	c.stderrTail = outputTail{} // zero value ready for use
	_ = trReader                // reader side of EOF is the pipe itself

	readCtx, genCancel := context.WithCancel(context.Background())

	c.spawnReadLoop(readCtx, genCancel)
	pw.Close() // agent stdout closes -> ReadAnyMessage returns io.EOF

	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		t.Fatal("readLoop did not exit within 3s after EOF")
	}

	if err := readCtx.Err(); err != context.Canceled {
		t.Fatalf("read ctx not cancelled after EOF return: err=%v (want context.Canceled)", err)
	}

	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if running {
		t.Fatal("client state still running after EOF recovery path (#1087 F3 expected running=false)")
	}
}
