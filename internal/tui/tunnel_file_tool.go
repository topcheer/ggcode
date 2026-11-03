package tui

import (
	"context"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

type mobileFileSenderAdapter struct {
	broker *tunnel.Broker
}

func (a mobileFileSenderAdapter) SendFileToMobile(ctx context.Context, path, caption string) (tool.MobileFileSendResult, error) {
	res, err := a.broker.SendFileToMobile(ctx, path, caption)
	if err != nil {
		return tool.MobileFileSendResult{}, err
	}
	return tool.MobileFileSendResult{
		FileID:   res.FileID,
		Filename: res.Filename,
		Mime:     res.Mime,
		Size:     res.Size,
		Chunks:   res.Chunks,
		SHA256:   res.SHA256,
	}, nil
}

func (m *Model) injectMobileFileSender(b *tunnel.Broker) {
	if m.agent == nil || b == nil {
		return
	}
	reg := m.agent.ToolRegistry()
	if reg == nil {
		return
	}
	t, ok := reg.Get(tool.MobileFileTool{}.Name())
	if !ok {
		return
	}
	mft, ok := t.(tool.MobileFileTool)
	if !ok {
		return
	}
	mft.Sender = mobileFileSenderAdapter{broker: b}

	reg.Unregister(mft.Name())
	if err := reg.Register(mft); err != nil {
		debug.Log("tui", "injectMobileFileSender: re-register failed: %v", err)
	}
}

func (m *Model) clearMobileFileSender() {
	if m.agent == nil {
		return
	}
	reg := m.agent.ToolRegistry()
	if reg == nil {
		return
	}
	t, ok := reg.Get(tool.MobileFileTool{}.Name())
	if !ok {
		return
	}
	if mft, ok := t.(tool.MobileFileTool); ok && mft.Sender != nil {
		mft.Sender = nil
		reg.Unregister(mft.Name())
		reg.Register(mft)
	}
}
