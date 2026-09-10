package provider

import "testing"

// Regression for #1618-A: non-text blocks make calibration samples
// asymmetric - the helper is the gate that keeps them out.
func TestMessagesContainNonTextBlocks(t *testing.T) {
	pure := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}
	if messagesContainNonTextBlocks(pure) {
		t.Fatal("text-only messages must not flag")
	}
	// #1795 case 2: tool_result WITHOUT embedded images is symmetric (the
	// local estimator counts its Output text) and must NOT flag - agent
	// sessions carry tool_result from the first tool call on, and the old
	// wholesale filter froze the calibration ratio on the first pure-text
	// sample for virtually every real agent session.
	for _, typ := range []string{"image", "thinking", "redacted_thinking"} {
		with := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}, {Type: typ}}}}
		if !messagesContainNonTextBlocks(with) {
			t.Fatalf("%s block must flag the message set", typ)
		}
	}
	withResult := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}, {Type: "tool_result", Output: "done"}}}}
	if messagesContainNonTextBlocks(withResult) {
		t.Fatal("plain tool_result (no embedded images) is symmetric - must not flag (#1795 case 2)")
	}
}
