package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #3675: constraint extraction must be user-source only. Assistant
// reasoning ("we must not X") and tool_result echoes (code comments,
// compiler errors) used to be lifted into the "treat as binding" section
// and self-perpetuate across compactions, locking the agent to its own
// past self-talk.
func Test3675UserConstraintSourceFiltersRoles(t *testing.T) {
	msgs := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "don't modify the existing tests"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "we must not touch internal/config"}}},
		{Role: "tool", Content: []provider.ContentBlock{{Type: "text", Text: "// never call this without a lock"}}},
	}
	src := userConstraintSource(msgs)
	if !strings.Contains(src, "don't modify the existing tests") {
		t.Fatalf("user constraint missing from source: %q", src)
	}
	if strings.Contains(src, "internal/config") || strings.Contains(src, "without a lock") {
		t.Fatalf("assistant/tool text leaked into user source: %q", src)
	}
}

func Test3675AssistantReasoningNotCrownedBinding(t *testing.T) {
	// The full pre-compaction payload DOES contain the assistant line;
	// the user source does not. Only the user constraint may be re-attached.
	payload := "=== CONVERSATION LOG ===\n[user] please add the feature\n[assistant] we must not touch internal/config - scope is UI only\n[user] don't modify the existing tests\n"
	userSrc := userConstraintSource([]provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "please add the feature\ndon't modify the existing tests"}}},
		{Role: "assistant", Content: []provider.ContentBlock{{Type: "text", Text: "we must not touch internal/config - scope is UI only"}}},
	})
	summary := "## Task\nAdd feature.\n## Done\nAdded."
	out := applyFactRetention(summary, payload, userSrc)
	if !strings.Contains(out, "don't modify the existing tests") {
		t.Fatalf("user constraint not re-attached:\n%s", out)
	}
	if strings.Contains(out, "must not touch internal/config") {
		t.Fatalf("assistant self-talk crowned as binding constraint (#3675 regression):\n%s", out)
	}
}

func Test3675ToolEchoNotCrownedBinding(t *testing.T) {
	// Compiler error / code comment echoes in tool output must not become
	// "user constraints".
	payload := "[tool] compile error: package must be imported as package y\n[user] keep it simple\n"
	userSrc := userConstraintSource([]provider.Message{
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "keep it simple"}}},
		{Role: "tool", Content: []provider.ContentBlock{{Type: "text", Text: "compile error: package must be imported as package y"}}},
	})
	out := applyFactRetention("## Task\nDone.", payload, userSrc)
	if strings.Contains(out, "imported as package y") {
		t.Fatalf("tool echo crowned as binding constraint:\n%s", out)
	}
}
