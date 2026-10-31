package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// zz_issue_subagent_notify: pin the P1-1 completion-push contract.
// 1. no feed -> no-op; 2. first observation is baseline (no inject);
// 3. NEW terminal agent -> ONE merged notice injected as user message;
// 4. repeated poll of same agent -> no duplicate; 5. summary truncated.

func newNotifyTestAgent(t *testing.T) *Agent {
	t.Helper()
	return NewAgent(&mockProvider{}, nil, "", 8)
}

func notifyTexts(ag *Agent) []string {
	var out []string
	for _, m := range ag.contextManager.Messages() {
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "sub-agent run finished") {
				out = append(out, b.Text)
			}
		}
	}
	return out
}

func TestSubagentNotifyNoFeedNoop(t *testing.T) {
	ag := newNotifyTestAgent(t)
	if ag.injectSubagentCompletions() {
		t.Fatal("no feed installed: must be a no-op")
	}
}

func TestSubagentNotifyBaselineThenPush(t *testing.T) {
	ag := newNotifyTestAgent(t)
	var feed func() []SubagentCompletion
	feed = func() []SubagentCompletion {
		return []SubagentCompletion{{ID: "sa-old", Name: "prior", Summary: "old result"}}
	}
	ag.SetSubagentCompletionFeed(feed)
	// First observation = baseline: marked seen, nothing injected.
	if ag.injectSubagentCompletions() {
		t.Fatal("baseline observation must not inject (pre-loop agents are not news)")
	}
	if n := len(notifyTexts(ag)); n != 0 {
		t.Fatalf("baseline injected %d notices, want 0", n)
	}
	// New completion arrives -> injected once.
	feed = func() []SubagentCompletion {
		return []SubagentCompletion{
			{ID: "sa-old", Name: "prior", Summary: "old result"},
			{ID: "sa-1", Name: "research", Summary: "found 3 refs"},
			{ID: "sa-2", Name: "audit", Failed: true, Summary: "boom"},
		}
	}
	ag.SetSubagentCompletionFeed(feed)
	if !ag.injectSubagentCompletions() {
		t.Fatal("new completions must inject")
	}
	texts := notifyTexts(ag)
	if len(texts) != 1 {
		t.Fatalf("want exactly ONE merged notice, got %d", len(texts))
	}
	txt := texts[0]
	for _, want := range []string{"sa-1", "sa-2", "research", "audit", "completed", "FAILED", "wait_agent"} {
		if !strings.Contains(txt, want) {
			t.Errorf("merged notice missing %q", want)
		}
	}
	if strings.Contains(txt, "sa-old") {
		t.Errorf("baseline agent leaked into notice")
	}
	// Poll again with same states -> no duplicate.
	if ag.injectSubagentCompletions() {
		t.Fatal("same terminal states must not re-notify")
	}
	if len(notifyTexts(ag)) != 1 {
		t.Fatal("duplicate notice injected")
	}
}

func TestSubagentNotifySummaryTruncated(t *testing.T) {
	ag := newNotifyTestAgent(t)
	long := strings.Repeat("x", 50) + "。" + strings.Repeat("y", 400)
	ag.SetSubagentCompletionFeed(func() []SubagentCompletion {
		return []SubagentCompletion{{ID: "sa-big", Name: "big", Summary: long}}
	})
	ag.injectSubagentCompletions() // baseline
	ag.SetSubagentCompletionFeed(func() []SubagentCompletion {
		return []SubagentCompletion{
			{ID: "sa-big2", Name: "big", Summary: long},
			{ID: "sa-big", Name: "big", Summary: long},
		}
	})
	if !ag.injectSubagentCompletions() {
		t.Fatal("expected injection")
	}
	texts := notifyTexts(ag)
	if len(texts) != 1 {
		t.Fatalf("want 1 notice, got %d", len(texts))
	}
	// 200-rune cap + "..." => the sa-big2 line must not carry the full 451-rune summary.
	if got := len([]rune(texts[0])); got > 600 {
		t.Errorf("notice too long (%d runes): summary truncation broken", got)
	}
}

// compile-time: provider import stays used by helper expectations.
var _ = provider.ContentBlock{Type: "text"}
