package agent

import (
	"strings"
	"testing"
	"time"
)

func TestTaskAnchor_NoFireBelowThreshold(t *testing.T) {
	s := newTaskAnchorState("Fix the bug in auth.go", time.Now())

	// Below threshold: no anchor should fire.
	for i := 0; i < anchorFirstFire-1; i++ {
		msg := s.maybeReanchorTask(i+1, 1, nil)
		if msg != "" {
			t.Fatalf("anchor fired at %d tool calls (below threshold %d)", i+1, anchorFirstFire)
		}
	}
}

func TestTaskAnchor_FireAtThreshold(t *testing.T) {
	s := newTaskAnchorState("Fix the bug in auth.go", time.Now())

	msg := s.maybeReanchorTask(anchorFirstFire, 3, []string{"auth.go"})
	if msg == "" {
		t.Fatal("expected anchor to fire at threshold")
	}

	// Verify the message contains the original prompt.
	if !strings.Contains(msg, "Fix the bug in auth.go") {
		t.Errorf("anchor message should contain original prompt, got: %s", msg)
	}

	// Verify it mentions tool call count.
	if !strings.Contains(msg, "10 tool calls") {
		t.Errorf("anchor should mention tool call count, got: %s", msg)
	}
}

func TestTaskAnchor_IntervalFiring(t *testing.T) {
	s := newTaskAnchorState("Implement feature X", time.Now())

	// First fire at threshold.
	msg1 := s.maybeReanchorTask(anchorFirstFire, 3, nil)
	if msg1 == "" {
		t.Fatal("expected first anchor")
	}

	// Should NOT fire again until anchorInterval more calls.
	for i := 1; i < anchorInterval; i++ {
		msg := s.maybeReanchorTask(anchorFirstFire+i, 3, nil)
		if msg != "" {
			t.Fatalf("anchor fired too early at %d calls after first anchor", i)
		}
	}

	// Should fire at anchorFirstFire + anchorInterval.
	msg2 := s.maybeReanchorTask(anchorFirstFire+anchorInterval, 5, nil)
	if msg2 == "" {
		t.Fatal("expected second anchor at interval")
	}
}

func TestTaskAnchor_TruncateLongPrompt(t *testing.T) {
	longPrompt := strings.Repeat("A very long task description. ", 100) // ~2800 chars
	s := newTaskAnchorState(longPrompt, time.Now())

	msg := s.maybeReanchorTask(anchorFirstFire, 3, nil)
	if msg == "" {
		t.Fatal("expected anchor message")
	}

	// The prompt in the message should be truncated.
	// Check that "..." is present (truncation marker).
	if !strings.Contains(msg, "...") {
		t.Error("expected truncation marker in anchor message for long prompt")
	}
}

func TestTaskAnchor_FilesEditedListed(t *testing.T) {
	s := newTaskAnchorState("Refactor module", time.Now())
	files := []string{"a.go", "b.go", "c.go"}

	msg := s.maybeReanchorTask(anchorFirstFire, 3, files)
	if msg == "" {
		t.Fatal("expected anchor message")
	}

	for _, f := range files {
		if !strings.Contains(msg, f) {
			t.Errorf("anchor should list file %s", f)
		}
	}
}

func TestTaskAnchor_FilesEditedTruncated(t *testing.T) {
	s := newTaskAnchorState("Big refactor", time.Now())
	files := make([]string, anchorMaxFiles+5)
	for i := range files {
		files[i] = "file" + string(rune('a'+i%26)) + ".go"
	}

	msg := s.maybeReanchorTask(anchorFirstFire, 3, files)
	if msg == "" {
		t.Fatal("expected anchor message")
	}

	// Should mention "total" count for truncated file list.
	if !strings.Contains(msg, "total") {
		t.Error("expected total file count in truncated file list")
	}
}

func TestTaskAnchor_Reset(t *testing.T) {
	s := newTaskAnchorState("First task", time.Now())

	// Fire once.
	_ = s.maybeReanchorTask(anchorFirstFire, 3, nil)
	if s.lastAnchoredAt != anchorFirstFire {
		t.Fatalf("expected lastAnchoredAt=%d, got %d", anchorFirstFire, s.lastAnchoredAt)
	}

	// Reset should clear state.
	s.reset("Second task", time.Now())
	if s.lastAnchoredAt != 0 {
		t.Fatalf("expected lastAnchoredAt=0 after reset, got %d", s.lastAnchoredAt)
	}
	if s.userPrompt != "Second task" {
		t.Errorf("expected user prompt reset, got %s", s.userPrompt)
	}

	// Should fire again at threshold after reset.
	msg := s.maybeReanchorTask(anchorFirstFire, 2, nil)
	if msg == "" {
		t.Fatal("expected anchor to fire after reset")
	}
	if !strings.Contains(msg, "Second task") {
		t.Error("anchor after reset should use new prompt")
	}
}

func TestTaskAnchor_DurationFormatting(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m30s"},
		{2 * time.Hour, "2h0m"},
	}
	for _, tt := range tests {
		got := formatAnchorDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatAnchorDuration(%v) = %s, want %s", tt.d, got, tt.want)
		}
	}
}

func TestTaskAnchor_EmptyPrompt(t *testing.T) {
	s := newTaskAnchorState("", time.Now())

	msg := s.maybeReanchorTask(anchorFirstFire, 3, nil)
	// Should still fire even with empty prompt.
	if msg == "" {
		t.Fatal("expected anchor to fire even with empty prompt")
	}
	// Should not contain "Original request:" with nothing after it
	// in a way that breaks formatting.
	if !strings.Contains(msg, "tool calls") {
		t.Error("anchor should mention tool calls regardless of prompt")
	}
}
