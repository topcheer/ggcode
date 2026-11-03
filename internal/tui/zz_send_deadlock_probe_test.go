package tui

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type probeSetProgMsg struct{ p *tea.Program }

type probeHoldMsg struct{}

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

		time.Sleep(3 * time.Second)
	}
	return m, nil
}

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

	time.Sleep(300 * time.Millisecond)

	p.Send(probeHoldMsg{})

	blocked := &atomic.Bool{}
	sendDone := make(chan struct{})
	go func() {
		p.Send(probePayloadMsg{})
		close(sendDone)
	}()

	select {
	case <-sendDone:
		if blocked.Load() {
			t.Fatal("impossible state")
		}
		t.Fatalf("Send was delivered while eventLoop was inside Update - " +
			"deadlock premise NOT reproduced; revisit bubbletea v2 eventLoop model")
	case <-time.After(1500 * time.Millisecond):

		t.Log("confirmed: program.Send blocks while eventLoop is busy in Update; " +
			"an in-Update Send (mcpPanelRefresh path) therefore self-deadlocks")
	}
}
