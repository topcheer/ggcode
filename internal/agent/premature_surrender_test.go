package agent

import (
	"testing"
)

func TestSurrenderDetection(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		currentIter int
		maxIter     int
		wantFire    bool
	}{
		{
			name:        "direct inability",
			text:        "I can't complete this task because the API is broken.",
			currentIter: 3,
			maxIter:     20,
			wantFire:    true,
		},
		{
			name:        "impossible claim",
			text:        "This is impossible to achieve with the current setup.",
			currentIter: 5,
			maxIter:     20,
			wantFire:    true,
		},
		{
			name:        "unable to complete",
			text:        "I'm unable to implement this feature.",
			currentIter: 2,
			maxIter:     15,
			wantFire:    true,
		},
		{
			name:        "no way to",
			text:        "There's no way to fix this without major refactoring.",
			currentIter: 3,
			maxIter:     10,
			wantFire:    true,
		},
		{
			name:        "skip for now",
			text:        "Let's skip this for now and come back later.",
			currentIter: 4,
			maxIter:     15,
			wantFire:    true,
		},
		{
			name:        "out of scope",
			text:        "This is out of scope for this PR.",
			currentIter: 2,
			maxIter:     10,
			wantFire:    true,
		},
		{
			name:        "beyond capabilities",
			text:        "This is beyond my capabilities to resolve.",
			currentIter: 3,
			maxIter:     10,
			wantFire:    true,
		},
		{
			name:        "this approach wont work",
			text:        "This approach won't work, we need to try something else.",
			currentIter: 2,
			maxIter:     10,
			wantFire:    true,
		},
		{
			name:        "normal progress - no surrender",
			text:        "I've implemented the feature and it works correctly.",
			currentIter: 3,
			maxIter:     10,
			wantFire:    false,
		},
		{
			name:        "legitimate at end - no budget left",
			text:        "I can't complete this task.",
			currentIter: 9,
			maxIter:     10,
			wantFire:    false,
		},
		{
			name:        "empty text",
			text:        "",
			currentIter: 3,
			maxIter:     10,
			wantFire:    false,
		},
		{
			name:        "normal analysis - no surrender",
			text:        "Let me analyze the code structure to understand how it works.",
			currentIter: 1,
			maxIter:     10,
			wantFire:    false,
		},
		{
			name:        "already fired - no re-fire",
			text:        "I can't do this.",
			currentIter: 3,
			maxIter:     10,
			wantFire:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSurrenderState()
			got := s.checkSurrender(tt.text, tt.currentIter, tt.maxIter)
			fired := got != ""
			if fired != tt.wantFire {
				t.Errorf("checkSurrender() fired=%v, wantFire=%v (msg=%q)", fired, tt.wantFire, got)
			}
		})
	}
}

func TestSurrenderFiresOnlyOnce(t *testing.T) {
	s := newSurrenderState()
	msg1 := s.checkSurrender("I can't complete this task.", 3, 20)
	if msg1 == "" {
		t.Fatal("expected first call to fire")
	}
	msg2 := s.checkSurrender("I can't complete this task.", 5, 20)
	if msg2 != "" {
		t.Fatal("expected second call to NOT fire (already fired)")
	}
}

func TestSurrenderReset(t *testing.T) {
	s := newSurrenderState()
	_ = s.checkSurrender("This is impossible.", 3, 20)
	if !s.fired {
		t.Fatal("expected to fire")
	}
	s.reset()
	if s.fired {
		t.Fatal("expected fired=false after reset")
	}
	if s.errorCount != 0 {
		t.Fatalf("expected errorCount=0 after reset, got %d", s.errorCount)
	}
	msg := s.checkSurrender("This is impossible.", 3, 20)
	if msg == "" {
		t.Fatal("expected to fire again after reset")
	}
}

func TestSurrenderRecordToolError(t *testing.T) {
	s := newSurrenderState()
	if s.errorCount != 0 {
		t.Fatalf("initial errorCount = %d, want 0", s.errorCount)
	}
	s.recordToolError()
	s.recordToolError()
	if s.errorCount != 2 {
		t.Fatalf("errorCount = %d, want 2", s.errorCount)
	}
}

func TestHasSurrenderPhrase(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"I can't do this", true},
		{"This seems impossible", true},
		{"Let's skip this for now", true},
		{"I'll implement this now", false},
		{"", false},
	}
	for _, tt := range tests {
		got := hasSurrenderPhrase(tt.text)
		if got != tt.want {
			t.Errorf("hasSurrenderPhrase(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
