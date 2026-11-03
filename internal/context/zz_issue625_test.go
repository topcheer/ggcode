package context

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

type issue625Provider struct {
	chatCalls  int
	lastPrompt string
}

func (p *issue625Provider) Name() string { return "issue625" }
func (p *issue625Provider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	p.chatCalls++
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" {
				p.lastPrompt = b.Text
			}
		}
	}
	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{{Type: "text", Text: "## Task\nFixed the parser bug per CRITICAL_DIRECTIVE_7f3a."}},
		},
		Usage: provider.TokenUsage{InputTokens: 100, OutputTokens: 50},
	}, nil
}
func (p *issue625Provider) ChatStream(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	close(ch)
	return ch, nil
}
func (p *issue625Provider) CountTokens(ctx context.Context, msgs []provider.Message) (int, error) {
	return 200, nil
}

func TestIssue625_SingleGroupEmbedsTriggerVerbatim(t *testing.T) {
	cm := NewManager(100000)
	prov := &issue625Provider{}

	task := "Fix the parser bug in module X. " +
		"CRITICAL_DIRECTIVE_7f3a: the fix MUST keep backward compatibility with the legacy config format and add regression tests for the boundary case where the delimiter is empty."
	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: task}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("working on it ", 40)}}})

	if err := cm.Summarize(context.Background(), prov); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if prov.chatCalls == 0 {
		t.Fatal("expected a summarization call")
	}
	if !strings.Contains(prov.lastPrompt, "TRIGGER MESSAGE VERBATIM") {
		t.Fatal("single-group compaction must embed the trigger message verbatim in the payload (#625)")
	}
	if !strings.Contains(prov.lastPrompt, "CRITICAL_DIRECTIVE_7f3a") {
		t.Fatal("verbatim trigger embed must contain the raw task directive")
	}
	if !strings.Contains(prov.lastPrompt, "backward compatibility with the legacy config format") {
		t.Fatal("verbatim trigger embed must carry the full task statement, not a one-line digest")
	}
}

func TestIssue625_MultiGroupNoTriggerEmbed(t *testing.T) {
	cm := NewManager(100000)
	prov := &issue625Provider{}

	cm.Add(provider.Message{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "System prompt."}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old question about topic A"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("old answer ", 40)}}})
	cm.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "recent question about topic B"}}})
	cm.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("recent answer ", 40)}}})

	if err := cm.Summarize(context.Background(), prov); err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if strings.Contains(prov.lastPrompt, "TRIGGER MESSAGE VERBATIM") {
		t.Fatal("multi-group compaction keeps the recent group verbatim — trigger embed must be absent")
	}
	msgs := cm.Messages()
	found := false
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "recent question about topic B") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("recent group must still be kept verbatim after multi-group compaction")
	}
}

func TestIssue625_TriggerEmbedTruncatesSafely(t *testing.T) {
	long := strings.Repeat("деталь задачи ", 900)
	if len([]rune(long)) <= triggerVerbatimMaxLen {
		t.Fatalf("precondition: probe must exceed %d runes", triggerVerbatimMaxLen)
	}
	got := headRunes(long, triggerVerbatimMaxLen)
	if !strings.Contains(got, "(truncated, original") {
		t.Fatal("expected truncation marker in headRunes output")
	}
	if !strings.HasPrefix(got, "деталь") {
		t.Fatal("headRunes must preserve the head of the original text")
	}
	for _, r := range got {
		if r == 0xFFFD {
			t.Fatal("headRunes must cut on rune boundaries")
		}
	}
}
