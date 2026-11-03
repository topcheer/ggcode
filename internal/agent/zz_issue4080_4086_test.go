package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue4080FetchAttributionUsesArgURL(t *testing.T) {
	o := &overseerState{}

	o.recordFinding("web_fetch", `{"url":"https://docs.example.com/guide"}`,
		"See https://cdn.example.net/logo.png and more text")
	f := o.researchFindings[len(o.researchFindings)-1]
	if f.URL != "https://docs.example.com/guide" {
		t.Fatalf("fetch attribution must be the arg URL, got %q", f.URL)
	}
	if hostOf(f.URL) != "docs.example.com" {
		t.Fatalf("hostOf: %q", hostOf(f.URL))
	}

	o.recordFinding("web_search", `{"query":"go generics"}`,
		"1. https://example.com/article - great intro\n2. ...")
	f2 := o.researchFindings[len(o.researchFindings)-1]
	if f2.URL != "https://example.com/article" {
		t.Fatalf("search attribution must stay body-first, got %q", f2.URL)
	}

	if hostOf("https://www.example.com/x") != hostOf("https://example.com/y") {
		t.Fatal("www-prefixed and bare hosts must compare equal")
	}

	got := unquoteJSONString(`{"url":"https://ex.com/a?q=\"hi\"&z=1"}`, "url")
	if got != `https://ex.com/a?q=\"hi\"&z=1` {
		t.Fatalf("escaped-quote value truncated: %q", got)
	}
}

func TestIssue4086TaskHintTakesLatestUser(t *testing.T) {
	a := newLLMOverseerTestAgent(t)
	a.contextManager.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "old task A from many turns ago"}}})
	a.contextManager.Add(provider.Message{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "did A"}}})
	a.contextManager.Add(provider.Message{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "current task B"}}})

	hint := llmOverseerTaskHint(a)
	if !strings.Contains(hint, "current task B") {
		t.Fatalf("taskHint must take the LATEST user message, got: %q", hint)
	}
	if strings.Contains(hint, "old task A") {
		t.Fatalf("taskHint must skip stale messages, got: %q", hint)
	}
	if strings.Contains(strings.ToLower(hint), "you are") {
		t.Fatalf("taskHint must never be the system prompt, got: %q", hint)
	}
}
