package agent

import (
	"os"
	"strings"
	"testing"
)

// #4622: the mindless-action measure must count reasoning output, not just
// visible text - reasoning models park thinking in thinkingAcc (bypassing
// textBuf), so len(textBuf) alone misreads reasoning-heavy tool-dense turns
// as "no thinking" and false-fires the detector.
func TestIssue4622_ReasoningCountedInMindlessMeasure(t *testing.T) {
	data, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "recordStep(len(textBuf)+reasoningChars, true)") {
		t.Fatal("recordStep must sum text and reasoning lengths (#4622)")
	}
	if !strings.Contains(text, "turnReasoningChars += len(event.Text)") {
		t.Fatal("reasoning handler must accumulate reasoning length (#4622)")
	}
}

func TestIssue4622_ReasoningBreaksMindlessStreak(t *testing.T) {
	s := newMindlessActionState()
	// 4 consecutive tool-bearing steps, each with 120 chars of reasoning and
	// no visible text: pre-fix every step counted as mindless and the 4th
	// fired a warning; with reasoning counted the streak must stay 0.
	for i := 0; i < 4; i++ {
		if s.recordStep(0+120, true) {
			t.Fatalf("step %d: reasoning-rich step must not fire the mindless warning (#4622)", i+1)
		}
	}
	if s.streak != 0 {
		t.Fatalf("streak=%d, want 0 when every step carries reasoning (#4622)", s.streak)
	}
}
