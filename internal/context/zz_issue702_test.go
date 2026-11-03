package context

import (
	"context"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue702a_OrphanCleanupClassifiesAsBenignTrim(t *testing.T) {
	m := newTestManager(t)

	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "orphan-1", Output: strings.Repeat("orphan output ", 30)},
	}})

	snap := m.CompactSnapshot()

	m.ReconcileToolCalls()
	if len(m.messages) != 2 {
		t.Fatalf("orphan message must be removed, live len=%d", len(m.messages))
	}

	applied, _ := m.ApplyCompactResult(snap, CompactResult{
		Changed:  true,
		Messages: []provider.Message{{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
	})
	if applied {
		t.Fatal("live-shrunk snapshot must be rejected")
	}
	if got := m.LastCompactRejectReason(); got != CompactRejectBenignTrim {
		t.Fatalf("after orphan cleanup, reject must be benign-trim, got %v", got)
	}
}

func TestIssue702a_UserClearClassifiesAsUserReset(t *testing.T) {
	m := newTestManager(t)
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	snap := m.CompactSnapshot()

	m.Clear()

	applied, _ := m.ApplyCompactResult(snap, CompactResult{
		Changed:  true,
		Messages: []provider.Message{{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: "summary"}}}},
	})
	if applied {
		t.Fatal("live-shrunk snapshot must be rejected")
	}
	if got := m.LastCompactRejectReason(); got != CompactRejectUserReset {
		t.Fatalf("after user Clear, reject must be user-reset, got %v", got)
	}
}

type midChatMutatorProvider struct {
	stubProvider
	onChat func()
}

func (p midChatMutatorProvider) Chat(ctx context.Context, msgs []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	if p.onChat != nil {
		p.onChat()
	}
	return p.stubProvider.Chat(ctx, msgs, tools)
}

func TestIssue702b_NoFalseChangedWhenVersionBumpsWithoutApply(t *testing.T) {
	m := newTestManager(t)

	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hi"}}})
	m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}})
	m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "tool_result", ToolID: "orphan-2", Output: strings.Repeat("orphan output ", 30)},
	}})

	prov := midChatMutatorProvider{onChat: func() { m.ReconcileToolCalls() }}

	changed, err := m.CheckAndSummarize(context.Background(), prov)
	if err != nil {
		t.Fatalf("CheckAndSummarize: %v", err)
	}
	if changed {
		t.Fatal("CheckAndSummarize must report changed=false when Summarize discarded its snapshot " +
			"(TOCTOU) and applied nothing — the version bump came from the benign mutation, not an applied summary")
	}
	for _, txt := range msgTexts(m) {
		if strings.Contains(txt, "stub summary") {
			t.Fatal("summary must not be applied on the TOCTOU-discard path")
		}
	}
}

func TestIssue702b_TrueChangeWhenApplied(t *testing.T) {
	m := NewManager(2000)
	for i := 0; i < 8; i++ {
		m.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: strings.Repeat("question ", 80)},
		}})
		m.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{
			{Type: "text", Text: strings.Repeat("answer ", 80)},
		}})
	}
	changed, err := m.CheckAndSummarize(context.Background(), stubProvider{})
	if err != nil {
		t.Fatalf("CheckAndSummarize: %v", err)
	}
	if !changed {
		t.Fatal("CheckAndSummarize must report changed=true when Summarize applied a summary")
	}
	if !strings.Contains(strings.Join(msgTexts(m), "\n"), "stub summary") {
		t.Fatal("summary message must be present after applied summarization")
	}
}

func msgTexts(m *Manager) []string {
	var out []string
	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Text != "" {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

func TestIssue702c_AnalyzeBudgetCJKNotUnderestimated(t *testing.T) {
	const runes = 200
	asciiMsg := provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "text", Text: strings.Repeat("a", runes)},
	}}
	cjkMsg := provider.Message{Role: "user", Content: []provider.ContentBlock{
		{Type: "text", Text: strings.Repeat("汉", runes)},
	}}

	asciiTokens := AnalyzeBudget([]provider.Message{asciiMsg}).TotalTokens
	cjkTokens := AnalyzeBudget([]provider.Message{cjkMsg}).TotalTokens

	if want := EstimateTokens(strings.Repeat("汉", runes)); cjkTokens != want {
		t.Fatalf("AnalyzeBudget CJK total = %d, want EstimateTokens = %d", cjkTokens, want)
	}
	if want := EstimateTokens(strings.Repeat("a", runes)); asciiTokens != want {
		t.Fatalf("AnalyzeBudget ASCII total = %d, want EstimateTokens = %d", asciiTokens, want)
	}
	if cjkTokens < asciiTokens*2 {
		t.Fatalf("CJK (%d tokens) must be ≥2x ASCII (%d tokens) for equal rune counts", cjkTokens, asciiTokens)
	}
}

func TestIssue702d_PinnedBudgetCountsRunes(t *testing.T) {
	p := newPinnedContext()

	cjk := strings.Repeat("汉", maxPinnedChars)
	for i := 0; i < 3; i++ {
		if _, err := p.Add(cjk); err != nil {
			t.Fatalf("item %d: rune-priced CJK item at the rune cap must be accepted: %v", i+1, err)
		}
	}

	id, err := p.Add(strings.Repeat("字", maxPinnedChars+50))
	if err != nil {
		t.Fatalf("over-cap item must be truncated-and-accepted: %v", err)
	}
	for _, item := range p.List() {
		if item.ID == id && len([]rune(item.Text)) != maxPinnedChars {
			t.Fatalf("over-cap item must be truncated to %d runes, got %d", maxPinnedChars, len([]rune(item.Text)))
		}
	}

	if _, err := p.Add("x"); err == nil {
		t.Fatal("total rune budget (8000) exceeded — Add must be rejected")
	}

	total := 0
	for _, item := range p.List() {
		total += len([]rune(item.Text))
	}
	if total != maxPinnedTotal {
		t.Fatalf("total pinned runes = %d, want exactly %d", total, maxPinnedTotal)
	}
}
