package context

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/provider"
)

// ContextManager manages conversation history, tracking tokens and auto-summarizing.
//
// ⚠️ Consuming packages must import this as "ctxpkg" to avoid
// collision with the standard library "context" package.
type ContextManager interface {
	Add(msg provider.Message)
	Messages() []provider.Message
	TokenCount() int
	MaxTokens() int
	SetMaxTokens(n int)
	Summarize(ctx context.Context, prov provider.Provider) error
	Clear()
	UsageRatio() float64
}

const (
	summarizeThreshold = 0.8
	recentMessages     = 6
	charsPerToken      = 4
)

// Manager implements ContextManager.
type Manager struct {
	mu        sync.Mutex
	messages  []provider.Message
	tokens    int
	maxTokens int
	provider  provider.Provider
}

// NewManager creates a ContextManager with the given context window limit.
func NewManager(maxTokens int) *Manager {
	return &Manager{maxTokens: maxTokens}
}

// SetProvider sets the provider for provider-aware token counting.
func (m *Manager) SetProvider(p provider.Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provider = p
}

func (m *Manager) Add(msg provider.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	m.tokens += m.countTokens(msg)
}

func (m *Manager) Messages() []provider.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]provider.Message, len(m.messages))
	copy(out, m.messages)
	return out
}

func (m *Manager) TokenCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens
}

func (m *Manager) MaxTokens() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.maxTokens
}

func (m *Manager) SetMaxTokens(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxTokens = n
}

func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		sys := m.messages[0]
		m.messages = []provider.Message{sys}
		m.tokens = m.countTokens(sys)
	} else {
		m.messages = nil
		m.tokens = 0
	}
}

func (m *Manager) UsageRatio() float64 {
	if m.maxTokens <= 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return float64(m.tokens) / float64(m.maxTokens)
}

// Summarize compresses old messages into a summary, keeping recent ones.
func (m *Manager) Summarize(ctx context.Context, prov provider.Provider) error {
	m.mu.Lock()

	sysIdx := -1
	if len(m.messages) > 0 && m.messages[0].Role == "system" {
		sysIdx = 0
	}

	nonRecentStart := 1
	if sysIdx < 0 {
		nonRecentStart = 0
	}
	oldEnd := len(m.messages) - recentMessages
	if oldEnd <= nonRecentStart {
		m.mu.Unlock()
		return nil
	}

	oldMsgs := make([]provider.Message, oldEnd-nonRecentStart)
	copy(oldMsgs, m.messages[nonRecentStart:oldEnd])

	origLen := len(m.messages)

	recentMsgs := make([]provider.Message, origLen-oldEnd)
	copy(recentMsgs, m.messages[oldEnd:])
	m.mu.Unlock()

	var sb strings.Builder
	for _, msg := range oldMsgs {
		sb.WriteString(fmt.Sprintf("[%s]\n", msg.Role))
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				sb.WriteString(block.Text)
				sb.WriteByte('\n')
			case "tool_use":
				sb.WriteString(fmt.Sprintf("Tool call: %s\n", block.ToolName))
			case "tool_result":
				sb.WriteString(fmt.Sprintf("Tool result: %s\n", block.Output))
			}
		}
		sb.WriteByte('\n')
	}

	summaryMsgs := []provider.Message{
		{
			Role: "system",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "Summarize the following conversation concisely, preserving key decisions and context. Output only the summary.",
			}},
		},
		{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: fmt.Sprintf("Summarize:\n\n%s", sb.String()),
			}},
		},
	}

	resp, err := prov.Chat(ctx, summaryMsgs, nil)
	if err != nil {
		return fmt.Errorf("summarization call failed: %w", err)
	}

	var summaryText string
	for _, block := range resp.Message.Content {
		if block.Type == "text" && block.Text != "" {
			summaryText = block.Text
			break
		}
	}
	if summaryText == "" {
		return fmt.Errorf("summarization returned empty text")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Collect any messages that arrived during summarization (TOCTOU fix)
	var extraMsgs []provider.Message
	if len(m.messages) > origLen {
		extraMsgs = make([]provider.Message, len(m.messages)-origLen)
		copy(extraMsgs, m.messages[origLen:])
	}

	newMsgs := make([]provider.Message, 0, len(recentMsgs)+len(extraMsgs)+2)
	if sysIdx >= 0 {
		newMsgs = append(newMsgs, m.messages[0])
	}
	newMsgs = append(newMsgs, provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: fmt.Sprintf("[Previous conversation summary]\n%s", summaryText),
		}},
	})
	newMsgs = append(newMsgs, recentMsgs...)
	newMsgs = append(newMsgs, extraMsgs...)

	m.messages = newMsgs
	m.recalcTokens()
	return nil
}

// CheckAndSummarize triggers summarization if usage ratio >= threshold.
func (m *Manager) CheckAndSummarize(ctx context.Context, prov provider.Provider) (bool, error) {
	if m.UsageRatio() >= summarizeThreshold {
		err := m.Summarize(ctx, prov)
		return err == nil, err
	}
	return false, nil
}

func (m *Manager) recalcTokens() {
	m.tokens = 0
	for _, msg := range m.messages {
		m.tokens += m.countTokens(msg)
	}
}

// countTokens uses the provider's token counting API when available,
// falling back to heuristic estimation.
func (m *Manager) countTokens(msg provider.Message) int {
	if m.provider != nil {
		if n, err := m.provider.CountTokens(context.Background(), []provider.Message{msg}); err == nil && n > 0 {
			return n
		}
	}
	return estimateTokens(msg)
}

func estimateTokens(msg provider.Message) int {
	var text string
	for _, b := range msg.Content {
		text += b.Text + b.ToolName + b.Output + string(b.Input)
	}
	return EstimateTokens(text)
}
