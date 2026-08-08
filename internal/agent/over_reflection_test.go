package agent

import (
	"strings"
	"testing"
)

func TestOverReflection_BelowThreshold(t *testing.T) {
	d := newOverReflectionDetector()
	shortText := "Let me check this."
	// Should not fire for short text with no tool calls
	hint := d.recordIteration(shortText, false, 1)
	if hint != "" {
		t.Fatalf("expected no hint for short text, got: %s", hint)
	}
	if d.consecutiveTextHeavy != 0 {
		t.Fatalf("expected 0 consecutive, got %d", d.consecutiveTextHeavy)
	}
}

func TestOverReflection_ResetOnToolCall(t *testing.T) {
	d := newOverReflectionDetector()
	longText := strings.Repeat("word ", 100)

	// First text-heavy turn, no tools
	hint := d.recordIteration(longText, false, 1)
	if hint != "" {
		t.Fatalf("expected no hint after 1 turn, got: %s", hint)
	}

	// Second text-heavy turn, no tools
	hint = d.recordIteration(longText, false, 2)
	if hint != "" {
		t.Fatalf("expected no hint after 2 turns, got: %s", hint)
	}

	// Tool call should reset the counter
	hint = d.recordIteration(longText, true, 3)
	if hint != "" {
		t.Fatalf("expected no hint after tool call, got: %s", hint)
	}
	if d.consecutiveTextHeavy != 0 {
		t.Fatalf("expected reset to 0, got %d", d.consecutiveTextHeavy)
	}
}

func TestOverReflection_WarnThreshold(t *testing.T) {
	d := newOverReflectionDetector()
	longText := strings.Repeat("deliberate ", 100) // >80 words

	var hint string
	for i := 1; i <= 3; i++ {
		hint = d.recordIteration(longText, false, i)
	}

	if hint == "" {
		t.Fatal("expected warning after 3 consecutive text-heavy no-action turns")
	}
	if !strings.Contains(hint, "Over-reflection warning") {
		t.Fatalf("expected warning text, got: %s", hint)
	}
}

func TestOverReflection_SevereThreshold(t *testing.T) {
	d := newOverReflectionDetector()
	longText := strings.Repeat("analyze ", 100)

	var hint string
	for i := 1; i <= 5; i++ {
		hint = d.recordIteration(longText, false, i)
	}

	if hint == "" {
		t.Fatal("expected severe warning after 5 consecutive text-heavy no-action turns")
	}
	if !strings.Contains(hint, "Over-reflection detected") {
		t.Fatalf("expected severe warning text, got: %s", hint)
	}
	if !strings.Contains(hint, "STOP planning") {
		t.Fatalf("expected action prompt in severe warning, got: %s", hint)
	}
}

func TestOverReflection_MaxWarnings(t *testing.T) {
	d := newOverReflectionDetector()
	longText := strings.Repeat("think ", 100)

	firedCount := 0
	for i := 1; i <= 10; i++ {
		hint := d.recordIteration(longText, false, i)
		if hint != "" {
			firedCount++
		}
	}

	// Should fire at most 2 times (warn + severe, each once)
	if firedCount != 2 {
		t.Fatalf("expected exactly 2 warnings (warn+severe), got %d", firedCount)
	}
}

func TestOverReflection_Reset(t *testing.T) {
	d := newOverReflectionDetector()
	longText := strings.Repeat("word ", 100)

	// Build up state
	for i := 1; i <= 4; i++ {
		d.recordIteration(longText, false, i)
	}
	if d.consecutiveTextHeavy != 4 {
		t.Fatalf("expected 4 consecutive before reset, got %d", d.consecutiveTextHeavy)
	}
	if !d.firedWarn {
		t.Fatal("expected warn to have fired")
	}

	// Reset
	d.reset()
	if d.consecutiveTextHeavy != 0 {
		t.Fatalf("expected 0 consecutive after reset, got %d", d.consecutiveTextHeavy)
	}
	if d.firedWarn {
		t.Fatal("expected firedWarn to be false after reset")
	}
	if d.firedSevere {
		t.Fatal("expected firedSevere to be false after reset")
	}
	if d.maxConsecutiveSeen != 0 {
		t.Fatalf("expected 0 maxConsecutive after reset, got %d", d.maxConsecutiveSeen)
	}
}

func TestCountWordsInText(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"   ", 0},
		{"one", 1},
		{"one two three", 3},
		{"one  two\tthree\nfour", 4},
		{strings.Repeat("a ", 50), 50},
	}
	for _, tt := range tests {
		got := countWordsInText(tt.input)
		if got != tt.expected {
			t.Errorf("countWordsInText(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestOverReflection_ShortTextResets(t *testing.T) {
	d := newOverReflectionDetector()
	longText := strings.Repeat("word ", 100)
	shortText := "ok"

	// Build up 2 consecutive text-heavy turns
	d.recordIteration(longText, false, 1)
	d.recordIteration(longText, false, 2)
	if d.consecutiveTextHeavy != 2 {
		t.Fatalf("expected 2 consecutive, got %d", d.consecutiveTextHeavy)
	}

	// Short text should reset
	d.recordIteration(shortText, false, 3)
	if d.consecutiveTextHeavy != 0 {
		t.Fatalf("expected reset after short text, got %d", d.consecutiveTextHeavy)
	}
}

func TestOverReflection_TracksMaxConsecutive(t *testing.T) {
	d := newOverReflectionDetector()
	longText := strings.Repeat("word ", 100)

	for i := 1; i <= 5; i++ {
		d.recordIteration(longText, false, i)
	}
	if d.maxConsecutiveSeen != 5 {
		t.Fatalf("expected maxConsecutive 5, got %d", d.maxConsecutiveSeen)
	}

	// Reset and build less
	d.reset()
	for i := 1; i <= 3; i++ {
		d.recordIteration(longText, false, i)
	}
	if d.maxConsecutiveSeen != 3 {
		t.Fatalf("expected maxConsecutive 3, got %d", d.maxConsecutiveSeen)
	}
}
