package provider

import "testing"

// Regression for #1618-A: non-text blocks make calibration samples
// asymmetric - the helper is the gate that keeps them out.
func TestMessagesContainNonTextBlocks(t *testing.T) {
	pure := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}
	if messagesContainNonTextBlocks(pure) {
		t.Fatal("text-only messages must not flag")
	}
	for _, typ := range []string{"image", "tool_result", "thinking", "redacted_thinking"} {
		with := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}, {Type: typ}}}}
		if !messagesContainNonTextBlocks(with) {
			t.Fatalf("%s block must flag the message set", typ)
		}
	}
}
