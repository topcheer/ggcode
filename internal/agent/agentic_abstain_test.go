package agent

import (
	"testing"
)

func TestAbstainDetection_FiresOnConsecutiveNegatives(t *testing.T) {
	s := newAbstainState()

	// Three consecutive negative results without acknowledgment
	s.recordResult("Error: file not found in /path", true)
	s.recordResult("Error: package not found", true)
	s.recordResult("Error: no such file or directory", true)

	// Should fire on the third negative
	s.recordAcknowledgment("") // agent doesn't acknowledge
	msg := s.checkAbstention(5, 20)
	if msg == "" {
		t.Fatal("expected abstention guidance after 3 consecutive negatives")
	}
	if !contains(msg, "Agentic Abstention") {
		t.Errorf("guidance should contain header, got: %s", msg)
	}
}

func TestAbstainDetection_ResetsOnAcknowledgment(t *testing.T) {
	s := newAbstainState()

	// Two negatives
	s.recordResult("not found", true)
	s.recordResult("no such file", true)
	s.recordAcknowledgment("The file was not found, so I'll try a different approach")

	// Acknowledgment should reset the counter
	if s.consecutiveNegatives != 0 {
		t.Errorf("expected consecutiveNegatives=0 after acknowledgment, got %d", s.consecutiveNegatives)
	}

	// Two more negatives shouldn't fire (only 2 consecutive after reset)
	s.recordResult("not found", true)
	s.recordResult("no such file", true)
	msg := s.checkAbstention(5, 20)
	if msg != "" {
		t.Errorf("should not fire after only 2 post-acknowledgment negatives, got: %s", msg)
	}
}

func TestAbstainDetection_ResetsOnPositiveResult(t *testing.T) {
	s := newAbstainState()

	// Two negatives then a positive
	s.recordResult("not found", true)
	s.recordResult("no such file", true)
	s.recordResult("package main\nfunc main() {}", false)

	// Positive result resets consecutive counter
	msg := s.checkAbstention(5, 20)
	if msg != "" {
		t.Errorf("should not fire after positive result interrupts negatives, got: %s", msg)
	}
}

func TestAbstainDetection_FiresOncePerRun(t *testing.T) {
	s := newAbstainState()

	for i := 0; i < 5; i++ {
		s.recordResult("not found", true)
	}
	s.recordAcknowledgment("")

	msg1 := s.checkAbstention(5, 20)
	if msg1 == "" {
		t.Fatal("expected first fire")
	}

	msg2 := s.checkAbstention(6, 20)
	if msg2 != "" {
		t.Error("should not fire twice in one run")
	}
}

func TestAbstainDetection_DoesNotFireOnLastIter(t *testing.T) {
	s := newAbstainState()

	for i := 0; i < 5; i++ {
		s.recordResult("not found", true)
	}
	s.recordAcknowledgment("")

	msg := s.checkAbstention(20, 20) // last iteration
	if msg != "" {
		t.Errorf("should not fire on last iteration, got: %s", msg)
	}
}

func TestHasNegativeSignal(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"Error: no such file or directory", true},
		{"file does not exist", true},
		{"package foo not found", true},
		{"404 Not Found", true},
		{"No results found", true},
		{"successfully created file", false},
		{"", false},
		{"everything works fine", false},
	}
	for _, tt := range tests {
		got := hasNegativeSignal(tt.content)
		if got != tt.want {
			t.Errorf("hasNegativeSignal(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

func TestHasAcknowledgment(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"The file doesn't exist, so I'll create it", true},
		{"This package is not available in this module", true},
		{"I can't find the function", true},
		{"The dependency is missing", true},
		{"Let me create the file now", false},
		{"", false},
	}
	for _, tt := range tests {
		got := hasAcknowledgment(tt.text)
		if got != tt.want {
			t.Errorf("hasAcknowledgment(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestAbstainDetection_Reset(t *testing.T) {
	s := newAbstainState()

	for i := 0; i < 3; i++ {
		s.recordResult("not found", true)
	}
	_ = s.checkAbstention(5, 20) // fire it
	if !s.fired {
		t.Fatal("expected fired=true before reset")
	}

	s.reset()
	if s.fired {
		t.Error("fired should be false after reset")
	}
	if s.consecutiveNegatives != 0 {
		t.Error("consecutiveNegatives should be 0 after reset")
	}
}
