package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactContextToolMetadata(t *testing.T) {
	tl := &CompactContextTool{}
	if tl.Name() != "compact_context" {
		t.Fatalf("unexpected tool name: %q", tl.Name())
	}
	if strings.TrimSpace(tl.Description()) == "" {
		t.Fatal("description must not be empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(tl.Parameters(), &schema); err != nil {
		t.Fatalf("parameters must be valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type must be object, got %v", schema["type"])
	}
}

func TestCompactContextToolNoRequester(t *testing.T) {
	tl := &CompactContextTool{}
	res, err := tl.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute returned system error: %v", err)
	}
	if !res.IsError {
		t.Fatal("missing requester must surface as a tool error")
	}
	if !strings.Contains(res.Content, "unavailable") {
		t.Fatalf("unexpected content: %q", res.Content)
	}
}

func TestCompactContextToolSchedulesAndPropagatesReason(t *testing.T) {
	var got string
	tl := &CompactContextTool{
		Requester: func(reason string) string {
			got = reason
			return "compaction scheduled"
		},
	}
	input, err := json.Marshal(map[string]string{"reason": "phase done"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	res, err := tl.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %q", res.Content)
	}
	if got != "phase done" {
		t.Fatalf("reason not propagated: %q", got)
	}
	if !strings.Contains(res.Content, "scheduled") {
		t.Fatalf("unexpected content: %q", res.Content)
	}
}

func TestCompactContextToolEmptyRequesterStatusIsError(t *testing.T) {
	tl := &CompactContextTool{Requester: func(string) string { return "" }}
	res, err := tl.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("empty requester status must surface as a tool error")
	}
}
