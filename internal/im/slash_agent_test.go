package im

import (
	"strings"
	"testing"
)

func TestExecuteAgentSlashCommand_Cost(t *testing.T) {
	called := false
	resp, handled := ExecuteAgentSlashCommand("/cost", AgentSlashOptions{
		OnCost: func() (string, error) { called = true; return "summary-text", nil },
	})
	if !handled || !called || resp != "summary-text" {
		t.Fatalf("cost: handled=%v called=%v resp=%q", handled, called, resp)
	}
	// nil hook degrades gracefully, still handled.
	resp, handled = ExecuteAgentSlashCommand("/cost", AgentSlashOptions{})
	if !handled || !strings.Contains(resp, "not available") {
		t.Fatalf("cost nil hook: handled=%v resp=%q", handled, resp)
	}
}

func TestExecuteAgentSlashCommand_Mode(t *testing.T) {
	var gotArg string
	resp, handled := ExecuteAgentSlashCommand("/mode bypass", AgentSlashOptions{
		OnMode: func(arg string) (string, error) { gotArg = arg; return "switched", nil },
	})
	if !handled || gotArg != "bypass" || resp != "switched" {
		t.Fatalf("mode switch: handled=%v arg=%q resp=%q", handled, gotArg, resp)
	}
	// No arg -> empty string passed through (show semantics live in handler).
	_, handled = ExecuteAgentSlashCommand("/mode", AgentSlashOptions{
		OnMode: func(arg string) (string, error) {
			if arg != "" {
				t.Fatalf("show form must pass empty arg, got %q", arg)
			}
			return "current: auto", nil
		},
	})
	if !handled {
		t.Fatal("mode show must be handled")
	}
}

func TestExecuteAgentSlashCommand_Unhandled(t *testing.T) {
	// Non-agent commands must fall through (handled=false) so the caller's
	// unknown-command path or other handlers take over.
	for _, text := range []string{"/restart", "/provider glm", "/help", "hello", ""} {
		if _, handled := ExecuteAgentSlashCommand(text, AgentSlashOptions{}); handled {
			t.Fatalf("%q must not be handled by agent slash", text)
		}
	}
}

func TestExecuteAgentSlashCommand_CaseInsensitive(t *testing.T) {
	resp, handled := ExecuteAgentSlashCommand("/COST", AgentSlashOptions{
		OnCost: func() (string, error) { return "ok", nil },
	})
	if !handled || resp != "ok" {
		t.Fatalf("case-insensitive: handled=%v resp=%q", handled, resp)
	}
}
