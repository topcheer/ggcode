package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptInjectorLateTextStillLands(t *testing.T) {
	a := newAutopilotTestAgent()

	a.baseSystemPrompt = "BASE PROMPT"
	a.SetSystemPromptInjector(func() string { return "" })

	a.maybeInjectDynamicSystemPrompt("hi")
	firstSys := firstSystemText(t, a)
	if !strings.Contains(firstSys, "BASE PROMPT") {
		t.Fatal("base prompt must be present after first pass")
	}
	if strings.Contains(firstSys, "SHARED WORKSPACE") {
		t.Fatal("empty injector must not inject alert text")
	}

	a.SetSystemPromptInjector(func() string {
		return "## SHARED WORKSPACE — CONCURRENT AGENTS ACTIVE\npeer-x is here"
	})
	a.maybeInjectDynamicSystemPrompt("continue")
	if got := firstSystemText(t, a); !strings.Contains(got, "SHARED WORKSPACE") || !strings.Contains(got, "peer-x") {
		t.Fatalf("late-converging injector text must reach the system prompt, got:\n%.400s", got)
	}

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
