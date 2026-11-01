package agent

import (
	"strings"
	"testing"
)

// #4917 regression pin: the shared-workspace alert flows through
// SetSystemPromptInjector into the system prompt, and — the actual bug —
// text produced LATE (lanchat presence converging after a restart/resume)
// still lands: the injector is re-evaluated on every injection pass, so an
// initially-empty probe that later reports peers must update the system
// message instead of being frozen out by the first empty evaluation.
func TestSystemPromptInjectorLateTextStillLands(t *testing.T) {
	a := newAutopilotTestAgent()
	// Production agents always carry a non-empty base prompt; the empty-
	// base path intentionally skips injection entirely (see skip guard).
	a.baseSystemPrompt = "BASE PROMPT"
	a.SetSystemPromptInjector(func() string { return "" })

	// First pass with no peers: prompt built, no alert content.
	a.maybeInjectDynamicSystemPrompt("hi")
	firstSys := firstSystemText(t, a)
	if !strings.Contains(firstSys, "BASE PROMPT") {
		t.Fatal("base prompt must be present after first pass")
	}
	if strings.Contains(firstSys, "SHARED WORKSPACE") {
		t.Fatal("empty injector must not inject alert text")
	}

	// Presence converges: same injector now reports a peer. The next
	// injection pass must fold the new text into the system message.
	a.SetSystemPromptInjector(func() string {
		return "## SHARED WORKSPACE — CONCURRENT AGENTS ACTIVE\npeer-x is here"
	})
	a.maybeInjectDynamicSystemPrompt("continue")
	if got := firstSystemText(t, a); !strings.Contains(got, "SHARED WORKSPACE") || !strings.Contains(got, "peer-x") {
		t.Fatalf("late-converging injector text must reach the system prompt, got:\n%.400s", got)
	}

	// Unchanged text must not churn (change-detection guard): a repeated
	// pass with identical output keeps lastInjectedSystemPrompt stable.
	before := a.lastInjectedSystemPrompt
	a.maybeInjectDynamicSystemPrompt("again")
	if a.lastInjectedSystemPrompt != before {
		t.Fatal("identical injector output must not re-trigger update")
	}
}

func firstSystemText(t *testing.T, a *Agent) string {
	t.Helper()
	for _, m := range a.contextManager.Messages() {
		if m.Role == "system" {
			var b strings.Builder
			for _, c := range m.Content {
				b.WriteString(c.Text)
			}
			return b.String()
		}
	}
	t.Fatal("no system message found")
	return ""
}
