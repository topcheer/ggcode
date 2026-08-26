package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/chat"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

// buildLongSessionModel creates a Model whose chatList holds a realistic
// long session: N turns of (user message + long assistant reply + tool call).
func buildLongSessionModel(turns int) Model {
	m := newTestModel()
	m.width = 180
	m.height = 52
	for i := 0; i < turns; i++ {
		m.chatWriteUserMarkdown(fmt.Sprintf("u-%d", i), fmt.Sprintf("第 %d 轮用户请求：请检查这个模块的性能问题并给出修复方案", i))
		a := chat.NewAssistantItem(fmt.Sprintf("a-%d", i), m.chatStyles)
		a.SetText(strings.Repeat(fmt.Sprintf("这是第 %d 轮助手回复的内容，包含分析与结论。", i), 30))
		a.SetFinished()
		m.chatList.Append(a)
		m.chatStartTool(ToolStatusMsg{
			ToolID:   fmt.Sprintf("t-%d", i),
			ToolName: "read_file",
			Running:  true,
			RawArgs:  fmt.Sprintf(`{"path":"/some/long/path/to/file/number/%d.go"}`, i),
		})
		m.chatFinishTool(ToolStatusMsg{
			ToolID:   fmt.Sprintf("t-%d", i),
			ToolName: "read_file",
			Running:  false,
			Result:   strings.Repeat("file content line\n", 120),
		})
	}
	return m
}

// BenchmarkView_LongSession measures full View() composition cost with a
// 200-turn session. Regression guard for the "long session renders sluggish"
// class of bugs: View is invoked once per bubbletea message, so per-frame
// cost directly gates input/scroll latency.
func BenchmarkView_LongSession(b *testing.B) {
	m := buildLongSessionModel(200)
	m.chatList.ScrollToEnd()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := m.View()
		_ = v
	}
}

// BenchmarkView_LongSessionScrolledUp measures View() cost mid-history
// (offset far from the tail).
func BenchmarkView_LongSessionScrolledUp(b *testing.B) {
	m := buildLongSessionModel(200)
	m.chatList.SetSize(m.conversationInnerWidth(), 40)
	m.chatList.ScrollUp(1500)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := m.View()
		_ = v
	}
}

// BenchmarkView_VeryLongSession scales to 1500 turns to expose O(n)-per-frame
// components that stay hidden at 200 turns.
func BenchmarkView_VeryLongSession(b *testing.B) {
	m := buildLongSessionModel(1500)
	m.chatList.ScrollToEnd()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := m.View()
		_ = v
	}
}

// BenchmarkView_LongSessionWithUsageHistory reproduces the real-world
// pathology of a very long session: tens of thousands of UsageHistory
// entries (one per streamed usage record). estimateSessionCost walks the
// entire history and calls resolveRate -> DefaultPricingTable() rebuild
// per entry, on EVERY View() frame.
func BenchmarkView_LongSessionWithUsageHistory(b *testing.B) {
	m := buildLongSessionModel(200)
	m.session = &session.Session{
		ID:       "bench",
		Vendor:   "zai",
		Endpoint: "cn-coding-anthropic",
		Model:    "glm-5.2",
	}
	m.session.TokenUsage = provider.TokenUsage{InputTokens: 1000, OutputTokens: 100}
	for i := 0; i < 74000; i++ {
		m.session.UsageHistory = append(m.session.UsageHistory, session.UsageEntry{
			Vendor:   "zai",
			Endpoint: "cn-coding-anthropic",
			Model:    "glm-5.2",
			Usage:    provider.TokenUsage{InputTokens: 10, OutputTokens: 1},
		})
	}
	m.chatList.ScrollToEnd()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := m.View()
		_ = v
	}
}

// BenchmarkEstimateSessionCost isolates the per-frame cost aggregation.
func BenchmarkEstimateSessionCost(b *testing.B) {
	m := newTestModel()
	m.session = &session.Session{
		ID:       "bench",
		Vendor:   "zai",
		Endpoint: "cn-coding-anthropic",
		Model:    "glm-5.2",
	}
	for i := 0; i < 74000; i++ {
		m.session.UsageHistory = append(m.session.UsageHistory, session.UsageEntry{
			Vendor:   "zai",
			Endpoint: "cn-coding-anthropic",
			Model:    "glm-5.2",
			Usage:    provider.TokenUsage{InputTokens: 10, OutputTokens: 1},
		})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.estimateSessionCost()
	}
}

// BenchmarkChatListRender_LongSession isolates the conversation list render.
func BenchmarkChatListRender_LongSession(b *testing.B) {
	m := buildLongSessionModel(200)
	m.chatList.SetSize(120, 40)
	m.chatList.ScrollToEnd()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.chatList.Render()
	}
}
