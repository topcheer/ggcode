package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4080: fetch-class source attribution. The argument URL IS the page's own
// URL - the body's first link (a CDN/asset link) must never preempt it.
// web_search keeps body-first (body = result list, first link = top result).
func TestIssue4080FetchAttributionUsesArgURL(t *testing.T) {
	o := &overseerState{}

	// Fetch: body contains an asset link; attribution must be the arg URL.
	o.recordFinding("web_fetch", `{"url":"https://docs.example.com/guide"}`,
		"See https://cdn.example.net/logo.png and more text")
	f := o.researchFindings[len(o.researchFindings)-1]
	if f.URL != "https://docs.example.com/guide" {
		t.Fatalf("fetch attribution must be the arg URL, got %q", f.URL)
	}
	if hostOf(f.URL) != "docs.example.com" {
		t.Fatalf("hostOf: %q", hostOf(f.URL))
	}

	// Search: body-first stays (result list semantics).
	o.recordFinding("web_search", `{"query":"go generics"}`,
		"1. https://example.com/article - great intro\n2. ...")
	f2 := o.researchFindings[len(o.researchFindings)-1]
	if f2.URL != "https://example.com/article" {
		t.Fatalf("search attribution must stay body-first, got %q", f2.URL)
	}

	// www-prefix normalization for distinct-host detection.
	if hostOf("https://www.example.com/x") != hostOf("https://example.com/y") {
		t.Fatal("www-prefixed and bare hosts must compare equal")
	}

	// Escaped quotes in JSON string values must not truncate.
	got := unquoteJSONString(`{"url":"https://ex.com/a?q=\"hi\"&z=1"}`, "url")
	if got != `https://ex.com/a?q=\"hi\"&z=1` {
		t.Fatalf("escaped-quote value truncated: %q", got)
	}
}

// #4086: the overseer's task baseline must be the most recent USER message,
// never msgs[0] (usually the system prompt) or a stale first task.
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
