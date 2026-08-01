package agent

import (
	"testing"
)

func TestFulfillmentGate_ActionRequestNoEdits(t *testing.T) {
	a := newFulfillmentTestAgent()
	stats := &RunStats{
		ToolCalls:  map[string]int{"read_file": 3, "grep": 2},
		UserPrompt: "add a new validation function to the user handler",
	}
	// No files edited, no commands run, but action verbs present
	msg := a.checkFulfillmentGate("add a new validation function to the user handler", stats, "")
	if msg == "" {
		t.Error("expected non-empty fulfillment gate message for action request with no edits")
	}
	if !a.fulfillmentGate.fired {
		t.Error("expected fulfillment gate to be marked as fired")
	}
}

func TestFulfillmentGate_InformationalSkipped(t *testing.T) {
	a := newFulfillmentTestAgent()
	stats := &RunStats{
		ToolCalls: map[string]int{"read_file": 2},
	}
	msg := a.checkFulfillmentGate("what does the agent loop do?", stats, "")
	if msg != "" {
		t.Errorf("expected empty message for informational request, got: %s", msg)
	}
}

func TestFulfillmentGate_AlreadyFired(t *testing.T) {
	a := newFulfillmentTestAgent()
	a.fulfillmentGate.fired = true
	stats := &RunStats{
		ToolCalls: map[string]int{"read_file": 1},
	}
	msg := a.checkFulfillmentGate("add a new function to handler.go", stats, "")
	if msg != "" {
		t.Error("expected empty message when gate already fired")
	}
}

func TestFulfillmentGate_EditsMatchKeywords(t *testing.T) {
	a := newFulfillmentTestAgent()
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"internal/handler/user.go"},
	}
	// Request mentions "user" and handler.go — keyword overlap should pass
	msg := a.checkFulfillmentGate("add validation to user handler", stats, "")
	if msg != "" {
		t.Errorf("expected gate to pass when edits match keywords, got: %s", msg)
	}
}

func TestFulfillmentGate_EditsNoKeywordOverlap(t *testing.T) {
	a := newFulfillmentTestAgent()
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"internal/README.md"},
	}
	msg := a.checkFulfillmentGate("fix the authentication bug in auth.go", stats, "")
	if msg == "" {
		t.Error("expected non-empty message when edited files have no keyword overlap with request")
	}
}

func TestFulfillmentGate_MultiPartRequest(t *testing.T) {
	a := newFulfillmentTestAgent()
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{"internal/config.go"},
	}
	// Multi-part request, only 1 file edited
	msg := a.checkFulfillmentGate("add rate limiting to the API and update the config file and fix the logging", stats, "")
	if msg == "" {
		t.Error("expected non-empty message for multi-part request with insufficient edits")
	}
}

func TestFulfillmentGate_ShortPromptSkipped(t *testing.T) {
	a := newFulfillmentTestAgent()
	stats := &RunStats{}
	msg := a.checkFulfillmentGate("hi", stats, "")
	if msg != "" {
		t.Error("expected empty message for short prompt")
	}
}

func TestFulfillmentGate_Reset(t *testing.T) {
	f := newFulfillmentGateState()
	f.fired = true
	f.reset()
	if f.fired {
		t.Error("expected fired to be false after reset")
	}
}

func TestIsInformationalRequest(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"what does this function do", true},
		{"explain how the agent loop works", true},
		{"add a new feature to the auth module", false},
		{"fix the bug in parser.go", false},
		{"how does the config system work", true},
	}
	for _, tt := range tests {
		got := isInformationalRequest(tt.input)
		if got != tt.want {
			t.Errorf("isInformationalRequest(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestDetectActions(t *testing.T) {
	actions := detectActions("add a new function and fix the bug")
	if len(actions) < 2 {
		t.Errorf("expected at least 2 actions, got %d: %v", len(actions), actions)
	}
}

func TestDetectMultiPart(t *testing.T) {
	tests := []struct {
		input string
		min   int
	}{
		{"add X and Y", 2},
		{"fix the bug then update the config", 2},
		{"1. add feature\n2. fix bug\n3. update docs", 3},
		{"add a simple function", 1},
	}
	for _, tt := range tests {
		got := detectMultiPart(tt.input)
		if got < tt.min {
			t.Errorf("detectMultiPart(%q) = %d, expected >= %d", tt.input, got, tt.min)
		}
	}
}

func TestExtractRequestKeywords(t *testing.T) {
	kw := extractRequestKeywords("fix the bug in handler.go and update config")
	// Should find "handler.go" at minimum
	found := false
	for _, k := range kw {
		if k == "handler.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find 'handler.go' in keywords: %v", kw)
	}
}

// newFulfillmentTestAgent creates a minimal agent for testing the fulfillment gate.
func newFulfillmentTestAgent() *Agent {
	a := &Agent{}
	a.fulfillmentGate = newFulfillmentGateState()
	return a
}
