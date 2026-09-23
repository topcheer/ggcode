package agent

import (
	"strings"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestWithTemporalContext(t *testing.T) {
	a := &Agent{}
	first := a.withTemporalContext("You are a coding assistant.")
	if !strings.HasPrefix(first, "Current date/time: ") {
		t.Fatalf("expected temporal header prefix, got: %q", first)
	}
	if !strings.Contains(first, "You are a coding assistant.") {
		t.Fatalf("base prompt lost, got: %q", first)
	}

	// Anchored: a second render must be byte-identical (KV-cache stability).
	second := a.withTemporalContext("You are a coding assistant.")
	if first != second {
		t.Fatalf("temporal header not stable across calls:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestWithTemporalContextEmptyBase(t *testing.T) {
	a := &Agent{}
	if got := a.withTemporalContext(""); got != "" {
		t.Fatalf("empty base must stay empty, got: %q", got)
	}
	if got := a.withTemporalContext("   "); got != "   " {
		t.Fatalf("whitespace-only base must stay unchanged, got: %q", got)
	}
}

func TestTemporalLineFormat(t *testing.T) {
	a := &Agent{}
	line := a.temporalContextLine()
	if !strings.Contains(line, "Current date/time:") {
		t.Fatalf("missing header in line: %q", line)
	}
	if !strings.Contains(line, "UTC offset") {
		t.Fatalf("missing UTC offset in line: %q", line)
	}
	if !strings.Contains(line, "current_time tool") {
		t.Fatalf("missing current_time tool hint in line: %q", line)
	}
	for _, weekday := range []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		if strings.Contains(line, weekday) {
			return
		}
	}
	t.Fatalf("no weekday in line: %q", line)
}

func TestMaybeInjectDynamicSystemPromptIncludesTemporal(t *testing.T) {
	a := NewAgent(nil, nil, "You are a coding assistant.", 1)
	a.maybeInjectDynamicSystemPrompt()
	// The injection path updates the context manager; verify via lastInjected cache.
	if !strings.Contains(a.lastInjectedSystemPrompt, "Current date/time: ") {
		t.Fatalf("temporal header missing from injected prompt: %q", a.lastInjectedSystemPrompt)
	}
	if !strings.Contains(a.lastInjectedSystemPrompt, "You are a coding assistant.") {
		t.Fatalf("base prompt missing from injected prompt: %q", a.lastInjectedSystemPrompt)
	}
}

// The temporal header is session-scoped (changes every session), so it must
// live OUTSIDE the cacheable base block - otherwise every session start (and
// every sub-agent) invalidates the cross-run cache breakpoint on the static
// system prompt prefix (arXiv:2601.06007).
func TestMaybeInjectDynamicSystemPromptTemporalOutsideCacheableBase(t *testing.T) {
	a := NewAgent(nil, nil, "You are a coding assistant.", 1)
	a.maybeInjectDynamicSystemPrompt()
	cm, ok := a.contextManager.(*ctxpkg.Manager)
	if !ok {
		t.Fatalf("context manager is not *ctxpkg.Manager")
	}
	var sys *provider.Message
	for _, m := range cm.Messages() {
		if m.Role == "system" {
			sys = &m
			break
		}
	}
	if sys == nil {
		t.Fatalf("no system message found after injection")
	}
	if len(sys.Content) < 2 {
		t.Fatalf("expected >=2 system blocks (cached base + temporal), got %d: %+v", len(sys.Content), sys.Content)
	}
	if sys.Content[0].Text != "You are a coding assistant." {
		t.Fatalf("cacheable block must be the bare base prompt (no temporal bytes), got: %q", sys.Content[0].Text)
	}
	if !sys.Content[0].Cache {
		t.Fatalf("base block must be marked cacheable")
	}
	if strings.Contains(sys.Content[0].Text, "Current date/time:") {
		t.Fatalf("temporal header leaked into cacheable base block: %q", sys.Content[0].Text)
	}
	if !strings.Contains(sys.Content[1].Text, "Current date/time:") {
		t.Fatalf("second block must carry the temporal header, got: %q", sys.Content[1].Text)
	}
	if sys.Content[1].Cache {
		t.Fatalf("temporal block must NOT be cacheable")
	}

	// Within one run the header is session-anchored: a second injection must
	// produce byte-identical blocks (KV-cache stability across iterations).
	a.maybeInjectDynamicSystemPrompt()
	for _, m := range cm.Messages() {
		if m.Role == "system" {
			for i := range m.Content {
				if m.Content[i].Text != sys.Content[i].Text {
					t.Fatalf("block %d changed across iterations: %q vs %q", i, sys.Content[i].Text, m.Content[i].Text)
				}
			}
			break
		}
	}
}

// A blank base with dynamic layers must not emit an empty cacheable block.
func TestMaybeInjectDynamicSystemPromptBlankBaseNoEmptyCachedBlock(t *testing.T) {
	a := NewAgent(nil, nil, "", 1)
	a.SetSystemPromptInjector(func() string { return "dynamic layer text" })
	a.maybeInjectDynamicSystemPrompt()
	cm, ok := a.contextManager.(*ctxpkg.Manager)
	if !ok {
		t.Fatalf("context manager is not *ctxpkg.Manager")
	}
	var sys *provider.Message
	for _, m := range cm.Messages() {
		if m.Role == "system" {
			sys = &m
			break
		}
	}
	if sys == nil {
		t.Fatalf("no system message found after injection")
	}
	for i, b := range sys.Content {
		if strings.TrimSpace(b.Text) == "" {
			t.Fatalf("block %d is empty/blank: %+v", i, b)
		}
		if b.Cache {
			t.Fatalf("block %d is cacheable but no stable base exists: %+v", i, b)
		}
	}
	if !strings.Contains(sys.Content[len(sys.Content)-1].Text, "dynamic layer text") {
		t.Fatalf("dynamic layer missing from system message: %+v", sys.Content)
	}
}
