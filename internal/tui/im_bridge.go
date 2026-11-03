package tui

import (
	"context"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
)

type remoteInboundMsg struct {
	Message  im.InboundMessage
	Response chan error
}

type imRuntimeUpdatedMsg struct{}

var tuiBridgeSubmitTimeout = 30 * time.Second

type tuiIMBridge struct {
	program  func() *tea.Program
	done     chan struct{}
	doneOnce sync.Once
}

func newTUIIMBridge(program func() *tea.Program) *tuiIMBridge {
	return &tuiIMBridge{program: program, done: make(chan struct{})}
}

func (b *tuiIMBridge) detach() {
	if b == nil {
		return
	}
	b.doneOnce.Do(func() { close(b.done) })
}

func (b *tuiIMBridge) SubmitInboundMessage(ctx context.Context, msg im.InboundMessage) error {
	if b == nil || b.program == nil {
		return fmt.Errorf("interactive program unavailable")
	}
	if b.isDetached() {
		return fmt.Errorf("interactive program unavailable")
	}
	prog := b.program()
	if prog == nil {
		return fmt.Errorf("interactive program unavailable")
	}
	resp := make(chan error, 1)
	prog.Send(remoteInboundMsg{Message: msg, Response: resp})
	return b.waitForResponse(ctx, resp)
}

func (b *tuiIMBridge) isDetached() bool {
	select {
	case <-b.done:
		return true
	default:
		return false
	}
}

func (b *tuiIMBridge) waitForResponse(ctx context.Context, resp <-chan error) error {
	timer := time.NewTimer(tuiBridgeSubmitTimeout)
	defer timer.Stop()
	select {
	case err := <-resp:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:

		return fmt.Errorf("tui program exited before processing inbound message")
	case <-timer.C:
		return fmt.Errorf("tui bridge submit timed out after %s", tuiBridgeSubmitTimeout)
	}
}

func buildRemoteInboundPrompt(msg im.InboundMessage) string {
	return im.BuildInboundText(msg)
}
