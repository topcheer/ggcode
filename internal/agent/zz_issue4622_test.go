package agent

import (
	"os"
	"strings"
	"testing"
)

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

	for i := 0; i < 4; i++ {
		if s.recordStep(0+120, true) {
			t.Fatalf("step %d: reasoning-rich step must not fire the mindless warning (#4622)", i+1)
		}
	}
	if s.streak != 0 {
		t.Fatalf("streak=%d, want 0 when every step carries reasoning (#4622)", s.streak)
	}
}
