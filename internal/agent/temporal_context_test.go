package agent

import (
	"strings"
	"testing"
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
