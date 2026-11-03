package acp

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

	routeAndExpect("sess-A", bufA)
	routeAndExpect("sess-B", bufB)
	if loopB.registry != loopA.registry {
		t.Fatal("loops A and B unexpectedly use different registries")
	}
	_ = loopC

}

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

func TestIssue1117_ReadLoopEOFCancelsStaleGeneration(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	trReader := strings.NewReader("")
	c := &Client{
		def:       DiscoveredAgent{Def: AgentDef{Name: "issue1117-agent"}},
		done:      make(chan struct{}),
		transport: NewTransport(pr, io.Discard),
	}
	c.stderrTail = outputTail{}
	_ = trReader

	readCtx, genCancel := context.WithCancel(context.Background())

	c.spawnReadLoop(readCtx, genCancel)
	pw.Close()

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
