package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
)

type remoteInboundMsg struct {
	Message  im.InboundMessage
	Response chan error
}

type imRuntimeUpdatedMsg struct{}

type tuiIMBridge struct {
	program func() *tea.Program
}

func newTUIIMBridge(program func() *tea.Program) *tuiIMBridge {
	return &tuiIMBridge{program: program}
}

func (b *tuiIMBridge) SubmitInboundMessage(ctx context.Context, msg im.InboundMessage) error {
	if b == nil || b.program == nil {
		return fmt.Errorf("interactive program unavailable")
	}
	prog := b.program()
	if prog == nil {
		return fmt.Errorf("interactive program unavailable")
	}
	resp := make(chan error, 1)
	prog.Send(remoteInboundMsg{Message: msg, Response: resp})
	select {
	case err := <-resp:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func buildRemoteInboundPrompt(msg im.InboundMessage) string {
	return im.BuildInboundText(msg)
}
