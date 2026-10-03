package runeval

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// r432 (arXiv:2608.12355 human-centered coding agents): interaction-quality
// dimensions in the run report - mid-run steering, ask_user deferrals, and
// explicit correction signals.

func TestSteeringCountsMidRunUserTextNotToolResults(t *testing.T) {
	msgs := []provider.Message{
		msg("user", provider.ContentBlock{Type: "text", Text: "initial task"}), // pre-agent: not steering
		msg("assistant", toolUse("t1", "git_status", `{}`)),
		msg("user", toolResult("t1", "clean", false)),                                         // tool-result user msg: not steering
		msg("user", provider.ContentBlock{Type: "text", Text: "actually also run the tests"}), // steering 1
		msg("assistant", toolUse("t2", "read_file", `{"path":"a.go"}`)),
		msg("user", toolResult("t2", "ok", false)),
		msg("user", provider.ContentBlock{Type: "text", Text: "  "}),                 // whitespace-only: not steering
		msg("user", provider.ContentBlock{Type: "text", Text: "focus on package b"}), // steering 2
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
	// ask_user answers arrive as tool results, not text - not steering.
	if r.SteeringEvents != 0 {
		t.Fatalf("SteeringEvents=%d, want 0", r.SteeringEvents)
	}
}

func TestCorrectionSignalsBilingualLexicon(t *testing.T) {
	msgs := []provider.Message{
		msg("user", provider.ContentBlock{Type: "text", Text: "refactor the parser"}),
		msg("assistant", provider.ContentBlock{Type: "text", Text: "refactored"}),
		msg("user", provider.ContentBlock{Type: "text", Text: "不对，保留公开 API"}), // correction 1 (zh)
		msg("assistant", provider.ContentBlock{Type: "text", Text: "adjusted"}),
		msg("user", provider.ContentBlock{Type: "text", Text: "that is Wrong, revert that"}), // correction 2 (en, case-insensitive)
		msg("user", provider.ContentBlock{Type: "text", Text: "neutral follow-up"}),          // steering but not correction
	}
	r := Evaluate(msgs, nil)
	if r.CorrectionSignals != 2 {
		t.Fatalf("CorrectionSignals=%d, want 2", r.CorrectionSignals)
	}
	if r.SteeringEvents != 3 {
		t.Fatalf("SteeringEvents=%d, want 3", r.SteeringEvents)
	}
	// >=2 corrections with <3 total steers? Here steers=3 so the combined
	// finding must fire and mention takeover.
	found := false
	for _, f := range r.Findings {
		if strings.Contains(f, "steer") || strings.Contains(f, "takeover") || strings.Contains(f, "correction") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an interaction-quality finding, got %v", r.Findings)
	}
	// Render must surface the interaction line.
	if out := Render(r); !strings.Contains(out, "Interaction:") {
		t.Fatalf("Render missing interaction line:\n%s", out)
	}
}
