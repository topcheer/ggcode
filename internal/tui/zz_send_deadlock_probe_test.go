package tui

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// probeSetProgMsg injects the program reference into the probe model,
// mirroring the production setProgramMsg mechanism.
type probeSetProgMsg struct{ p *tea.Program }

// probeHoldMsg parks the eventLoop inside Update for a fixed duration -
// standing in for the synchronous MCPManager.Refresh call chain.
type probeHoldMsg struct{}

// probePayloadMsg is the message delivered via a bare program.Send.
type probePayloadMsg struct{}

type sendDeadlockProbe struct {
	prog *tea.Program
}

func (m *sendDeadlockProbe) Init() tea.Cmd { return nil }
func (m *sendDeadlockProbe) View() tea.View {
	return tea.NewView("probe")
}
func (m *sendDeadlockProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case probeSetProgMsg:
		m.prog = typed.p
	case probeHoldMsg:
		// Occupies the eventLoop: during this window the loop cannot consume
		// from p.msgs, which is exactly the state while a synchronous
		// Update -> Refresh -> ... -> program.Send chain is in flight.
		time.Sleep(3 * time.Second)
	}
	return m, nil
}

// TestProbe_SendBlocksWhileUpdateBusy reproduces the mechanism behind the
// MCP panel f-key TUI freeze (PR #4603 root cause):
//
//	bubbletea v2: msgs = make(chan Msg)          (tea.go:606, UNBUFFERED)
//	Program.Send: select{<-ctx.Done()/msgs<-msg} (tea.go:1192, NO default)
//	eventLoop:    sole consumer, runs Update INLINE (tea.go:760)
//
// When Update synchronously calls something that ends in program.Send
// (mcpPanelRefresh -> MCPManager.Refresh -> emitUpdate -> onUpdate ->
// repl.go:1558 program.Send), the Send waits on a consumer that is stuck
// inside that very Update - a permanent deadlock, unfixable by any timeout,
// only killable via process death. This probe demonstrates the primitive:
// while the eventLoop is inside Update, a bare Send blocks.
func TestProbe_SendBlocksWhileUpdateBusy(t *testing.T) {
	if testing.Short() {
		t.Skip("probe needs a real program loop")
	}
	m := &sendDeadlockProbe{}
	p := tea.NewProgram(m,
		tea.WithInput(&bytes.Buffer{}),
		tea.WithOutput(io.Discard),
	)
	done := make(chan struct{})
	go func() { _, _ = p.Run(); close(done) }()
	defer func() {
		p.Quit()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			p.Kill()
		}
	}()

	time.Sleep(300 * time.Millisecond) // let the loop start

	// Step 1: occupy the eventLoop inside Update for 3s.
	p.Send(probeHoldMsg{})

	// Step 2: from OUTSIDE, a bare Send during the occupied window.
	blocked := &atomic.Bool{}
	sendDone := make(chan struct{})
	go func() {
		p.Send(probePayloadMsg{})
		close(sendDone)
	}()

	// Step 3: after 1.5s (loop still busy for another 1.5s) the Send must
	// still be pending if the channel is unbuffered with no fallback.
	select {
	case <-sendDone:
		if blocked.Load() {
			t.Fatal("impossible state")
		}
		t.Fatalf("Send was delivered while eventLoop was inside Update - " +
			"deadlock premise NOT reproduced; revisit bubbletea v2 eventLoop model")
	case <-time.After(1500 * time.Millisecond):
		// Still blocked: the primitive is confirmed. Combined with the source
		// chain (Update -> Refresh -> emitUpdate -> onUpdate -> program.Send
		// called from INSIDE Update in mcpPanelRefresh), this is the reported
		// permanent TUI freeze.
		t.Log("confirmed: program.Send blocks while eventLoop is busy in Update; " +
			"an in-Update Send (mcpPanelRefresh path) therefore self-deadlocks")
	}
}
