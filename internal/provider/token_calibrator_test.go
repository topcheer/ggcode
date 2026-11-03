package provider

import "testing"

func TestMessagesContainNonTextBlocks(t *testing.T) {
	pure := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}
	if messagesContainNonTextBlocks(pure) {
		t.Fatal("text-only messages must not flag")
	}

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
