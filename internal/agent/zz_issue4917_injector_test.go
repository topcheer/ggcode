package agent

import (
	"strings"
	"testing"
)

// #4917 pinned that late-converging injector text still lands in the prompt.
// #5807 relocated the injector output from the system head into the volatile
// turn context (per-prompt segments must not rewrite messages[0]); the
// landing guarantee now applies to the turn-context render.
func TestSystemPromptInjectorLateTextStillLands(t *testing.T) {
	a := newAutopilotTestAgent()

	a.baseSystemPrompt = "BASE PROMPT"
	a.SetSystemPromptInjector(func() string { return "" })

	if got := a.renderVolatileTurnContext("hi"); got != "" {
		t.Fatalf("empty injector must yield no volatile turn context, got:\n%s", got)
	}

	a.SetSystemPromptInjector(func() string {
		return "## SHARED WORKSPACE — CONCURRENT AGENTS ACTIVE\npeer-x is here"
	})
	vol := a.renderVolatileTurnContext("continue")
	if !strings.Contains(vol, "SHARED WORKSPACE") || !strings.Contains(vol, "peer-x") {
		t.Fatalf("late-converging injector text must reach the volatile turn context, got:\n%.400s", vol)
	}

	a.maybeInjectStableSystemHead()
	got := firstSystemText(t, a)
	if !strings.Contains(got, "BASE PROMPT") {
		t.Fatalf("base prompt must be present in the stable head, got:\n%.400s", got)
	}
	if strings.Contains(got, "SHARED WORKSPACE") || strings.Contains(got, "peer-x") {
		t.Fatalf("injector output must not rewrite the stable system head (#5807), got:\n%.400s", got)
	}

	before := a.lastInjectedSystemPrompt
	a.maybeInjectStableSystemHead()
	if a.lastInjectedSystemPrompt != before {
		t.Fatal("identical stable head parts must not re-trigger head update")
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
