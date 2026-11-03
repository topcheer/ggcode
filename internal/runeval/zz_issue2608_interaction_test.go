package runeval

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestSteeringCountsMidRunUserTextNotToolResults(t *testing.T) {
	msgs := []provider.Message{
		msg("user", provider.ContentBlock{Type: "text", Text: "initial task"}),
		msg("assistant", toolUse("t1", "git_status", `{}`)),
		msg("user", toolResult("t1", "clean", false)),
		msg("user", provider.ContentBlock{Type: "text", Text: "actually also run the tests"}),
		msg("assistant", toolUse("t2", "read_file", `{"path":"a.go"}`)),
		msg("user", toolResult("t2", "ok", false)),
		msg("user", provider.ContentBlock{Type: "text", Text: "  "}),
		msg("user", provider.ContentBlock{Type: "text", Text: "focus on package b"}),
	}
	r := Evaluate(msgs, nil)
	if r.SteeringEvents != 2 {
		t.Fatalf("SteeringEvents=%d, want 2 (initial prompt, tool results, and whitespace-only excluded)", r.SteeringEvents)
	}
	if r.CorrectionSignals != 0 {
		t.Fatalf("CorrectionSignals=%d, want 0", r.CorrectionSignals)
	}
	if r.AskUserCalls != 0 {
		t.Fatalf("AskUserCalls=%d, want 0", r.AskUserCalls)
	}
}

func TestAskUserDeferralsCounted(t *testing.T) {
	msgs := []provider.Message{
		msg("user", provider.ContentBlock{Type: "text", Text: "do the thing"}),
		msg("assistant", toolUse("q1", "ask_user", `{"title":"which db"}`)),
		msg("user", toolResult("q1", "postgres", false)),
		msg("assistant", toolUse("q2", "ask_user", `{"title":"confirm"}`)),
		msg("user", toolResult("q2", "yes", false)),
		msg("assistant", provider.ContentBlock{Type: "text", Text: "done"}),
	}
	r := Evaluate(msgs, nil)
	if r.AskUserCalls != 2 {
		t.Fatalf("AskUserCalls=%d, want 2", r.AskUserCalls)
	}

	if r.SteeringEvents != 0 {
		t.Fatalf("SteeringEvents=%d, want 0", r.SteeringEvents)
	}
}

func TestCorrectionSignalsBilingualLexicon(t *testing.T) {
	msgs := []provider.Message{
		msg("user", provider.ContentBlock{Type: "text", Text: "refactor the parser"}),
		msg("assistant", provider.ContentBlock{Type: "text", Text: "refactored"}),
		msg("user", provider.ContentBlock{Type: "text", Text: "不对，保留公开 API"}),
		msg("assistant", provider.ContentBlock{Type: "text", Text: "adjusted"}),
		msg("user", provider.ContentBlock{Type: "text", Text: "that is Wrong, revert that"}),
		msg("user", provider.ContentBlock{Type: "text", Text: "neutral follow-up"}),
	}
	r := Evaluate(msgs, nil)
	if r.CorrectionSignals != 2 {
		t.Fatalf("CorrectionSignals=%d, want 2", r.CorrectionSignals)
	}
	if r.SteeringEvents != 3 {
		t.Fatalf("SteeringEvents=%d, want 3", r.SteeringEvents)
	}

	found := false
	for _, f := range r.Findings {
		if strings.Contains(f, "steer") || strings.Contains(f, "takeover") || strings.Contains(f, "correction") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an interaction-quality finding, got %v", r.Findings)
	}

	if out := Render(r); !strings.Contains(out, "Interaction:") {
		t.Fatalf("Render missing interaction line:\n%s", out)
	}
}
