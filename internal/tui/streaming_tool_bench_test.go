package tui

import (
	"fmt"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

type streamBodySetter interface {
	SetStreamingBody(string)
}

func streamingToolModel(turns int) (Model, streamBodySetter) {
	m := buildLongSessionModel(turns)
	m.session = &session.Session{
		ID:       "bench",
		Vendor:   "zai",
		Endpoint: "cn-coding-anthropic",
		Model:    "glm-5.2",
		TokenUsage: provider.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 100,
		},
	}
	for i := 0; i < 74000; i++ {
		m.session.UsageHistory = append(m.session.UsageHistory, session.UsageEntry{
			Vendor: "zai", Endpoint: "cn-coding-anthropic", Model: "glm-5.2",
			Usage: provider.TokenUsage{InputTokens: 10, OutputTokens: 1},
		})
	}
	m.chatStartTool(ToolStatusMsg{
		ToolID:   "stream-tool",
		ToolName: "run_command",
		Running:  true,
		Detail:   "long build streaming output",
	})
	var setter streamBodySetter
	if it := m.chatList.FindByID("stream-tool"); it != nil {
		setter, _ = it.(streamBodySetter)
	}
	return m, setter
}

func BenchmarkView_RunningStreamingTool(b *testing.B) {
	m, setter := streamingToolModel(200)
	if setter == nil {
		b.Fatal("streaming tool item not found")
	}

	tail := ""
	for i := 0; i < 5; i++ {
		tail += fmt.Sprintf("[...] src/module%03d/component.go:123: compiling dependency graph node %d\n", i, i*37)
	}
	setter.SetStreamingBody(tail)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {

		chat.SetToolAnimFrame(i % 4)
		if i%2 == 0 {
			setter.SetStreamingBody(tail + fmt.Sprintf("line %d\n", i))
		}
		v := m.View()
		_ = v
	}
}

func BenchmarkToolItemRunningRender(b *testing.B) {
	m, setter := streamingToolModel(1)
	if setter == nil {
		b.Fatal("streaming tool item not found")
	}
	setter.SetStreamingBody("[...] compiling dependency graph, artifacts pending resolve\n")
	renderer, ok := m.chatList.FindByID("stream-tool").(interface {
		Render(int) string
	})
	if !ok {
		b.Fatal("tool item does not implement Render")
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		chat.SetToolAnimFrame(i % 4)
		_ = renderer.Render(120)
	}
}

func BenchmarkUpdateByID_LongList(b *testing.B) {
	m, setter := streamingToolModel(600)
	if setter == nil {
		b.Fatal("streaming tool item not found")
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.chatUpdateToolOutput("stream-tool", fmt.Sprintf("[...] tick %d\n", i))
		m.chatListFollowOutput()
	}
}
