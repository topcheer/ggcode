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

// tuiBridgeSubmitTimeout bounds how long SubmitInboundMessage waits for the
// TUI Update loop to acknowledge an inbound message (#4587: callers pass
// context.Background(), and after the program quits prog.Send is a no-op, so
// the resp channel is never written - without a bound the adapter goroutine
// blocks forever and leaks one goroutine per inbound message).
var tuiBridgeSubmitTimeout = 30 * time.Second

type tuiIMBridge struct {
	program  func() *tea.Program
	done     chan struct{}
	doneOnce sync.Once
}

func newTUIIMBridge(program func() *tea.Program) *tuiIMBridge {
	return &tuiIMBridge{program: program, done: make(chan struct{})}
}

// detach marks the bridge as no longer backed by a live TUI program.
// In-flight and future SubmitInboundMessage calls return an error instead of
// waiting out the full timeout. Safe to call multiple times.
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

// waitForResponse blocks until the TUI Update loop answers, the caller's
// context is cancelled, the bridge is detached (the program it belonged to
// quit), or the submit timeout elapses. Every branch returns - this is the
// #4587 fix: no path blocks forever.
func (b *tuiIMBridge) waitForResponse(ctx context.Context, resp <-chan error) error {
	timer := time.NewTimer(tuiBridgeSubmitTimeout)
	defer timer.Stop()
	select {
	case err := <-resp:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		// The TUI program this bridge served has been replaced or quit; the
		// queued remoteInboundMsg will never be processed. Report the drop
		// so the adapter goroutine returns promptly.
		return fmt.Errorf("tui program exited before processing inbound message")
	case <-timer.C:
		return fmt.Errorf("tui bridge submit timed out after %s", tuiBridgeSubmitTimeout)
	}
}

func buildRemoteInboundPrompt(msg im.InboundMessage) string {
	return im.BuildInboundText(msg)
}
