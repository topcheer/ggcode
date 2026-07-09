package provider

import (
	"testing"
)

func TestMergeSystemMessages_NoSystem(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}
	result := MergeSystemMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
}

func TestMergeSystemMessages_SingleSystemAtStart(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "sys"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hello"}}},
	}
	result := MergeSystemMessages(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	if result[0].Role != "system" || result[1].Role != "user" {
		t.Fatalf("unexpected order: %s, %s", result[0].Role, result[1].Role)
	}
}

func TestMergeSystemMessages_MultipleSystemMergedIntoOne(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "sys1"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "msg1"}}},
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "sys2"}}},
		{Role: "assistant", Content: []ContentBlock{{Type: "text", Text: "reply"}}},
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "sys3"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "msg2"}}},
	}
	result := MergeSystemMessages(msgs)

	// Expected: 1 merged system + 3 non-system = 4
	if len(result) != 4 {
		t.Fatalf("expected 4, got %d", len(result))
	}

	// First must be system
	if result[0].Role != "system" {
		t.Fatalf("expected first to be system, got %s", result[0].Role)
	}

	// All three system texts merged into first message
	if len(result[0].Content) != 3 {
		t.Fatalf("expected 3 content blocks in merged system, got %d", len(result[0].Content))
	}
	if result[0].Content[0].Text != "sys1" || result[0].Content[1].Text != "sys2" || result[0].Content[2].Text != "sys3" {
		t.Fatalf("unexpected system texts: %v", result[0].Content)
	}

	// Non-system messages preserve order
	if result[1].Role != "user" || result[2].Role != "assistant" || result[3].Role != "user" {
		t.Fatalf("unexpected non-system order: %s %s %s", result[1].Role, result[2].Role, result[3].Role)
	}
}

func TestMergeSystemMessages_EmptyInput(t *testing.T) {
	result := MergeSystemMessages(nil)
	if len(result) != 0 {
		t.Fatalf("expected empty, got %d", len(result))
	}
}

func TestMergeSystemMessages_SingleMessage(t *testing.T) {
	msgs := []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}}}
	result := MergeSystemMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
}

func TestMergeSystemMessages_EmptySystemTextsFiltered(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: ""}, {Type: "text", Text: "  "}}},
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "real"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}
	result := MergeSystemMessages(msgs)

	// Empty system texts should be filtered, only "real" survives
	if result[0].Role != "system" {
		t.Fatalf("expected system, got %s", result[0].Role)
	}
	if len(result[0].Content) != 1 || result[0].Content[0].Text != "real" {
		t.Fatalf("expected only 'real' block, got %v", result[0].Content)
	}
}

func TestMergeSystemMessages_OnlySystemMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "a"}}},
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "b"}}},
	}
	result := MergeSystemMessages(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 merged system, got %d", len(result))
	}
	if len(result[0].Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result[0].Content))
	}
}

func TestMergeSystemMessages_PreservesCacheHint(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "cached", Cache: true}}},
		{Role: "system", Content: []ContentBlock{{Type: "text", Text: "dynamic"}}},
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}
	result := MergeSystemMessages(msgs)
	if !result[0].Content[0].Cache {
		t.Fatal("expected cache hint preserved on first block")
	}
	if result[0].Content[1].Cache {
		t.Fatal("expected no cache hint on second block")
	}
}
