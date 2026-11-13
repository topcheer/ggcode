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

func TestDetectActions_QuotedReferenceExempt(t *testing.T) {
	// #5554: verbs inside a #NNNN-referencing bracket citation are quoted
	// material (a commit title), not session instructions.
	got := detectActions("二进制同步令：main 已至 21e9549（含 #5508 counterfactual repair 腿，CI 绿）。凡 19:18Z 后换过二进制的席请立即 restart。")
	for _, v := range got {
		if v == "repair" {
			t.Errorf("expected 'repair' inside #NNNN bracket citation to be exempt, got actions: %v", got)
		}
	}
}

func TestDetectActions_QuotedTitleExempt(t *testing.T) {
	// #5554: verbs inside quoted commit titles are exempt (straight quotes).
	got := detectActions("A binary-sync order body quotes a commit title containing '#5508 counterfactual repair'.")
	for _, v := range got {
		if v == "repair" {
			t.Errorf("expected 'repair' inside quoted title to be exempt, got actions: %v", got)
		}
	}
}

func TestDetectActions_BareInstructionStillCounts(t *testing.T) {
	// Negative control: bare "fix #1234" is a real instruction and must NOT
	// be exempt (no quotes, no reference-carrying bracket around the verb).
	got := detectActions("please fix #1234 as soon as possible")
	found := false
	for _, v := range got {
		if v == "fix" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected bare instruction verb 'fix' to still be detected, got: %v", got)
	}
}

func TestFulfillmentGate_SyncOrderQuotedTitleNoIntervention(t *testing.T) {
	// Issue #5554 acceptance: a sync-order body quoting arbitrary commit
	// titles produces zero completion-checker interventions when the actual
	// request (restart to load) is already complete (tools ran, no edits).
	a := newFulfillmentTestAgent()
	stats := &RunStats{
		ToolCalls: map[string]int{"restart": 1, "run_command": 1},
	}
	body := "[二进制同步令·P0] main 已至 21e9549（含 #5508 counterfactual repair：D2 named-return toolDefs 未赋值→LLM 零工具定义；修复已 CI 绿+本席重启实证工具链恢复）。凡换过二进制的席请立即 restart 加载修复版。"
	if msg := a.checkFulfillmentGate(body, stats, ""); msg != "" {
		t.Errorf("expected zero interventions for sync-order with quoted titles, got: %s", msg)
	}
}

func TestFulfillmentGate_RealActionStillFires(t *testing.T) {
	// Negative control: a real edit request with zero edits/commands must
	// still fire — the exemption must not swallow genuine instructions.
	a := newFulfillmentTestAgent()
	stats := &RunStats{
		ToolCalls: map[string]int{"read_file": 3},
	}
	msg := a.checkFulfillmentGate("add a retry wrapper around the provider call", stats, "")
	if msg == "" {
		t.Error("expected fulfillment gate to fire for real action request with no edits")
	}
}

func newFulfillmentTestAgent() *Agent {
	a := &Agent{}
	a.fulfillmentGate = newFulfillmentGateState()
	return a
}
