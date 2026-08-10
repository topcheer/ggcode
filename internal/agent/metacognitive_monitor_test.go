package agent

import (
	"testing"
)

func TestMetacognitiveMonitor_Basic(t *testing.T) {
	m := newMetacognitiveMonitor()
	if m == nil {
		t.Fatal("newMetacognitiveMonitor returned nil")
	}

	// Record consistent turns (same plan, no contradictions)
	m.recordTurn("read_file", "read config file", "the config needs updating")
	m.recordTurn("read_file", "read config file", "the config needs updating")
	m.recordTurn("read_file", "read config file", "the config needs updating")

	// Should not trigger intervention (consistency is high)
	if hint := m.maybeIntervene(); hint != "" {
		t.Errorf("Expected no intervention, got: %s", hint)
	}
}

func TestMetacognitiveMonitor_Contradiction(t *testing.T) {
	m := newMetacognitiveMonitor()

	// Record contradictory turns (need metaMinTurns=3 to trigger)
	// Use add/delete which is an actual opposite pair in isMetaOppositeAction
	m.recordTurn("read_file", "add file X", "this file is necessary")
	m.recordTurn("edit_file", "delete file X", "this file is broken")
	m.recordTurn("delete_file", "add file X again", "this file is necessary again")

	// Should detect contradiction
	if hint := m.maybeIntervene(); hint == "" {
		t.Error("Expected intervention for contradiction")
	}
}

func TestMetacognitiveMonitor_Reset(t *testing.T) {
	m := newMetacognitiveMonitor()

	m.recordTurn("read_file", "read file", "interpretation")
	m.recordTurn("edit_file", "edit file", "interpretation")

	m.reset()

	if len(m.turns) != 0 {
		t.Errorf("Expected 0 turns after reset, got %d", len(m.turns))
	}
	if m.consistencyScore != 1.0 {
		t.Errorf("Expected consistency 1.0 after reset, got %f", m.consistencyScore)
	}
}

func TestMetaExtractActionType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"read file X", "read"},
		{"edit file Y", "edit"},
		{"search for pattern", "search"},
		{"build the project", "build"},
		{"unknown action", ""},
	}

	for _, tt := range tests {
		if got := metaExtractActionType(tt.input); got != tt.expected {
			t.Errorf("metaExtractActionType(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMetaExtractTarget(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"read file X", "X"},
		{"edit pattern Y in file", "Y"},
		{"search directory Z", "Z"},
		{"no target", ""},
	}

	for _, tt := range tests {
		if got := metaExtractTarget(tt.input); got != tt.expected {
			t.Errorf("metaExtractTarget(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestIsMetaOppositeAction(t *testing.T) {
	tests := []struct {
		a        string
		b        string
		expected bool
	}{
		{"delete file X", "create file X", true},
		{"enable feature", "disable feature", true},
		{"read file", "write file", false},
		{"edit file", "edit file", false},
	}

	for _, tt := range tests {
		if got := isMetaOppositeAction(tt.a, tt.b); got != tt.expected {
			t.Errorf("isMetaOppositeAction(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.expected)
		}
	}
}
