package context

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// r122: directed compaction — "/compact <focus>" must inject the user's
// directives into the summarization system prompt as highest-priority
// guidance, while the no-focus path keeps the prompt unchanged.

type focusCapturingProvider struct {
	mockProvider
	lastMsgs []provider.Message
}

func (m *focusCapturingProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	m.lastMsgs = msgs
	return m.mockProvider.Chat(ctx, msgs, tools)
}

func summarizeFocusTestMsgs() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "oldest question"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "oldest answer"}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "recent question"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "recent answer"}}},
	}
}

func TestSummarizeMessages_FocusInjectedIntoSystemPrompt(t *testing.T) {
	prov := &focusCapturingProvider{}
	focus := "keep the API design discussion and the failure-cascade decisions"
	if _, err := summarizeMessages(context.Background(), prov, summarizeFocusTestMsgs(), nil, 10000, "", focus); err != nil {
		t.Fatalf("summarizeMessages failed: %v", err)
	}
	if len(prov.lastMsgs) == 0 || prov.lastMsgs[0].Role != "system" {
		t.Fatal("expected system message in summarization request")
	}
	sysText := prov.lastMsgs[0].Content[0].Text
	if !strings.Contains(sysText, "User Compaction Focus Directives") {
		t.Fatal("expected focus section header in system prompt")
	}
	if !strings.Contains(sysText, focus) {
		t.Fatalf("expected focus text %q verbatim in system prompt", focus)
	}
}

func TestSummarizeMessages_NoFocusKeepsPromptClean(t *testing.T) {
	prov := &focusCapturingProvider{}
	if _, err := summarizeMessages(context.Background(), prov, summarizeFocusTestMsgs(), nil, 10000, "", ""); err != nil {
		t.Fatalf("summarizeMessages failed: %v", err)
	}
	if len(prov.lastMsgs) == 0 || prov.lastMsgs[0].Role != "system" {
		t.Fatal("expected system message in summarization request")
	}
	sysText := prov.lastMsgs[0].Content[0].Text
	if strings.Contains(sysText, "User Compaction Focus Directives") {
		t.Fatal("empty focus must not inject the focus section")
	}
}

func TestSummarizeWithFocus_PassesFocusThrough(t *testing.T) {
	prov := &focusCapturingProvider{}
	cm := NewManager(120)
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("question ", 30)}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("answer ", 30)}}})
	focus := "preserve all release-process decisions"
	if err := cm.SummarizeWithFocus(context.Background(), prov, focus); err != nil {
		t.Fatalf("SummarizeWithFocus failed: %v", err)
	}
	var sysText string
	for _, m := range prov.lastMsgs {
		if m.Role == "system" {
			sysText = m.Content[0].Text
			break
		}
	}
	if !strings.Contains(sysText, focus) {
		t.Fatal("expected SummarizeWithFocus to forward focus into the summarization system prompt")
	}
}
