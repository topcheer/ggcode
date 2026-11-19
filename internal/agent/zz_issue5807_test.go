package agent

import (
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// newIssue5807Agent builds an agent whose volatile inputs are deterministic:
// a fixed injector, no layers, and an in-memory rule store carrying one rule
// per intent so different prompts classify to different rule sets.
func newIssue5807Agent(t *testing.T) (*Agent, *RuleStore) {
	t.Helper()
	a := NewAgent(nil, nil, "BASE PROMPT", 1)
	td := t.TempDir()
	rs := &RuleStore{
		path:       filepath.Join(td, ".ggcode", "agent-rules.json"),
		maxRules:   defaultMaxRules,
		regexCache: map[string]*regexp.Regexp{},
		rules: []Rule{
			{ID: "r-test-a", Rule: "RULE-FOR-TEST-INTENT-A", TaskType: "test", HitCount: 5, LastSeen: time.Now(), CreatedAt: time.Now()},
			{ID: "r-test-b", Rule: "RULE-FOR-TEST-INTENT-B", TaskType: "test", HitCount: 4, LastSeen: time.Now(), CreatedAt: time.Now()},
			{ID: "r-refactor-a", Rule: "RULE-FOR-REFACTOR-INTENT-A", TaskType: "refactor", HitCount: 5, LastSeen: time.Now(), CreatedAt: time.Now()},
			{ID: "r-refactor-b", Rule: "RULE-FOR-REFACTOR-INTENT-B", TaskType: "refactor", HitCount: 4, LastSeen: time.Now(), CreatedAt: time.Now()},
		},
	}
	a.mu.Lock()
	a.ruleStore = rs
	a.workingDir = td
	a.mu.Unlock()
	a.SetSystemPromptInjector(func() string { return "PEER ALERT peer-x active" })
	return a, rs
}

// issue5807Turn mirrors the D1 wiring: volatile context rendered from the
// run prompt, attached to the new user turn, then the stable head assembled.
func issue5807Turn(t *testing.T, a *Agent, cm *ctxpkg.Manager, prompt string) string {
	t.Helper()
	vol := a.renderVolatileTurnContext(prompt)
	cm.AppendUserTurnWithVolatileTail(provider.Message{Role: "user"},
		[]provider.ContentBlock{{Type: "text", Text: prompt}}, vol)
	a.maybeInjectStableSystemHead()
	return vol
}

// TestIssue5807HeadByteStableAcrossIntents pins the core regression: two
// same-session runs with different intents must keep the system head
// byte-identical, with the per-prompt segments riding each new user turn.
func TestIssue5807HeadByteStableAcrossIntents(t *testing.T) {
	a, _ := newIssue5807Agent(t)
	cm := a.contextManager.(*ctxpkg.Manager)

	vol1 := issue5807Turn(t, a, cm, "add coverage for the auth tests")
	headAfterTurn1 := append([]provider.ContentBlock(nil), cm.Messages()[0].Content...)

	vol2 := issue5807Turn(t, a, cm, "refactor the parser and rename symbols")

	if !strings.Contains(vol1, "RULE-FOR-TEST-INTENT-A") || strings.Contains(vol1, "RULE-FOR-REFACTOR-INTENT-A") {
		t.Fatalf("turn-1 volatile context must carry only the test-intent rule:\n%s", vol1)
	}
	if !strings.Contains(vol2, "RULE-FOR-REFACTOR-INTENT-A") || strings.Contains(vol2, "RULE-FOR-TEST-INTENT-A") {
		t.Fatalf("turn-2 volatile context must carry only the refactor-intent rule:\n%s", vol2)
	}
	if !strings.Contains(vol1, "PEER ALERT") || !strings.Contains(vol2, "PEER ALERT") {
		t.Fatal("injector output must ride the volatile turn context")
	}
	if !strings.HasPrefix(vol1, volatileTurnContextHeader) {
		t.Fatalf("volatile context must be delimited by the turn-context header:\n%s", vol1)
	}

	msgs := cm.Messages()
	if len(msgs) != 3 {
		t.Fatalf("want head + 2 user turns, got %d messages", len(msgs))
	}
	headAfterTurn2 := msgs[0].Content
	if !reflect.DeepEqual(headAfterTurn1, headAfterTurn2) {
		t.Fatal("system head must stay byte-identical across user turns with different intents (#5807)")
	}

	head := msgs[0]
	if head.Role != "system" || len(head.Content) != 1 || !head.Content[0].Cache {
		t.Fatalf("stable head must be one cache-flagged system block at the stable boundary: role=%s blocks=%d", head.Role, len(head.Content))
	}
	headText := head.Content[0].Text
	if !strings.Contains(headText, "BASE PROMPT") || !strings.Contains(headText, "Current date/time: ") {
		t.Fatalf("stable head lost base/temporal segments:\n%.300s", headText)
	}
	for _, forbidden := range []string{"PEER ALERT", "RULE-FOR-TEST-INTENT-A", "RULE-FOR-REFACTOR-INTENT-A", volatileTurnContextHeader} {
		if strings.Contains(headText, forbidden) {
			t.Fatalf("stable head must not carry per-prompt segment %q (#5807)", forbidden)
		}
	}

	last := msgs[2]
	if last.Role != "user" || len(last.Content) != 2 {
		t.Fatalf("new user turn must carry the volatile trailing block: role=%s blocks=%d", last.Role, len(last.Content))
	}
	if got := last.Content[1].Text; got != vol2 {
		t.Fatal("trailing block must be the turn-2 volatile context verbatim")
	}
	if !last.Content[1].Cache {
		t.Fatal("the newest volatile block must carry the live cache breakpoint")
	}
	if msgs[1].Content[1].Cache {
		t.Fatal("the turn-1 volatile block must have its cache flag rotated off")
	}
	if msgs[1].Content[1].Text != vol1 {
		t.Fatal("turn-1 volatile block bytes must stay frozen after rotation")
	}
}

// TestIssue5807PromptCacheMetricsLogged pins the observability ask: head/tail
// token split and prefix-cache invalidation events surface under the
// prompt-cache debug category, and a stable head does not re-fire them.
func TestIssue5807PromptCacheMetricsLogged(t *testing.T) {
	var mu sync.Mutex
	var events []string
	debug.SetLiveSink(func(category, msg string) {
		if strings.HasPrefix(msg, "[prompt-cache]") {
			mu.Lock()
			events = append(events, msg)
			mu.Unlock()
		}
	})
	defer debug.SetLiveSink(nil)

	a, _ := newIssue5807Agent(t)
	cm := a.contextManager.(*ctxpkg.Manager)

	issue5807Turn(t, a, cm, "add coverage for the auth tests")

	mu.Lock()
	var sawHead, sawTail, sawInvalidation bool
	for _, e := range events {
		if strings.Contains(e, "stable system head assembled: headTokens=") {
			sawHead = true
		}
		if strings.Contains(e, "volatile turn context attached: tailTokens=") {
			sawTail = true
		}
		if strings.Contains(e, "prefix-cache invalidation event") {
			sawInvalidation = true
		}
	}
	if !sawHead || !sawTail || !sawInvalidation {
		mu.Unlock()
		t.Fatalf("prompt-cache metrics missing (head=%t tail=%t invalidation=%t): %v", sawHead, sawTail, sawInvalidation, events)
	}

	n := len(events)
	mu.Unlock()

	issue5807Turn(t, a, cm, "refactor the parser and rename symbols")

	mu.Lock()
	defer mu.Unlock()
	for _, e := range events[n:] {
		if strings.Contains(e, "prefix-cache invalidation event") {
			t.Fatalf("a new user turn must not fire a head invalidation event: %s", e)
		}
	}
}
