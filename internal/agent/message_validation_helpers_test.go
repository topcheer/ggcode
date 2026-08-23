package agent

import (
	"reflect"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestContainsToolResultBlock(t *testing.T) {
	tests := []struct {
		name    string
		content []provider.ContentBlock
		want    bool
	}{
		{"empty", nil, false},
		{"text only", []provider.ContentBlock{{Type: "text", Text: "hi"}}, false},
		{"tool_use only", []provider.ContentBlock{{Type: "tool_use", ToolID: "t1"}}, false},
		{"with tool_result", []provider.ContentBlock{
			{Type: "text", Text: "hi"},
			{Type: "tool_result", ToolID: "t1"},
		}, true},
	}
	for _, tt := range tests {
		if got := containsToolResultBlock(tt.content); got != tt.want {
			t.Errorf("%s: containsToolResultBlock = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestFilterToolResults(t *testing.T) {
	open := []openCall{
		{id: "t1", name: "read_file"},
		{id: "t2", name: "grep"},
	}
	content := []provider.ContentBlock{
		{Type: "text", Text: "guidance"},
		{Type: "tool_result", ToolID: "t2", Output: "ok"},
		{Type: "tool_result", ToolID: "orphan", Output: "bad"},
	}

	kept, updated, dropped := filterToolResults(content, open)

	if dropped != true {
		t.Fatalf("expected orphan to be dropped")
	}
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept blocks (text + matching tool_result), got %d", len(kept))
	}
	if kept[0].Type != "text" || kept[1].ToolID != "t2" {
		t.Fatalf("unexpected kept blocks: %+v", kept)
	}
	if len(updated) != 1 || updated[0].id != "t1" {
		t.Fatalf("expected open list to shrink to [t1], got %+v", updated)
	}

	// All-closing batch leaves the open list empty.
	kept2, updated2, dropped2 := filterToolResults([]provider.ContentBlock{
		{Type: "tool_result", ToolID: "t1"},
	}, updated)
	if dropped2 || len(kept2) != 1 || len(updated2) != 0 {
		t.Fatalf("unexpected second pass: kept=%d open=%d dropped=%v", len(kept2), len(updated2), dropped2)
	}
}

func TestStripToolResultImages(t *testing.T) {
	orig := []provider.ContentBlock{
		{Type: "tool_result", ToolID: "t1", Output: "with image", Images: []provider.ContentImage{{MIME: "image/png"}}},
		{Type: "text", Text: "keep"},
		{Type: "tool_result", ToolID: "t2", Output: "no image"},
	}
	msg := provider.Message{Role: "user", Content: orig}

	stripped, count := stripToolResultImages(msg)

	if count != 1 {
		t.Fatalf("expected 1 stripped block, got %d", count)
	}
	if len(stripped.Content) != 3 {
		t.Fatalf("expected 3 blocks preserved, got %d", len(stripped.Content))
	}
	got := stripped.Content[0]
	if got.ToolID != "t1" || got.Output != "with image" || len(got.Images) != 0 {
		t.Fatalf("image block not stripped correctly: %+v", got)
	}
	if !reflect.DeepEqual(stripped.Content[1], orig[1]) || !reflect.DeepEqual(stripped.Content[2], orig[2]) {
		t.Fatalf("non-image blocks must pass through unchanged")
	}
	// Caller's slice must never be mutated.
	if len(orig[0].Images) != 1 {
		t.Fatalf("original slice was mutated: %+v", orig[0])
	}
}
