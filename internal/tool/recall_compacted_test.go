package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRecallCompactedToolMetadata(t *testing.T) {
	tl := &RecallCompactedTool{}
	if tl.Name() != "recall_compacted" {
		t.Fatalf("unexpected tool name: %q", tl.Name())
	}
	if strings.TrimSpace(tl.Description()) == "" {
		t.Fatal("description must not be empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(tl.Parameters(), &schema); err != nil {
		t.Fatalf("parameters must be valid JSON: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema must have a properties object, got %T", schema["properties"])
	}
	if _, ok := props["query"]; !ok {
		t.Fatal("schema must declare 'query'")
	}
	req, ok := schema["required"].([]any)
	if !ok || len(req) == 0 || req[0] != "query" {
		t.Fatalf("'query' must be required, got %v", schema["required"])
	}
}

func TestRecallCompactedToolNoSearcher(t *testing.T) {
	tl := &RecallCompactedTool{}
	res, err := tl.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute returned system error: %v", err)
	}
	if !res.IsError {
		t.Fatal("missing searcher must surface as a tool error")
	}
	if !strings.Contains(res.Content, "unavailable") {
		t.Fatalf("unexpected content: %q", res.Content)
	}
}

func TestRecallCompactedToolPassesQueryAndDefaultLimit(t *testing.T) {
	var gotQuery string
	var gotLimit int
	tl := &RecallCompactedTool{
		Searcher: func(query string, limit int) string {
			gotQuery, gotLimit = query, limit
			return "hit: old output"
		},
	}
	input, err := json.Marshal(map[string]any{"query": "Error 1"})
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
	if gotQuery != "Error 1" {
		t.Fatalf("query not propagated: %q", gotQuery)
	}
	if gotLimit != 5 {
		t.Fatalf("default limit must be 5, got %d", gotLimit)
	}
}

func TestRecallCompactedToolExplicitLimitAndEmptyResult(t *testing.T) {
	tl := &RecallCompactedTool{
		Searcher: func(query string, limit int) string { return "" },
	}
	input, _ := json.Marshal(map[string]any{"query": "nothing", "limit": 3})
	res, err := tl.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatal("empty result is a success (no match), not an error")
	}
	if !strings.Contains(res.Content, "No archived compacted content matched") {
		t.Fatalf("unexpected no-match content: %q", res.Content)
	}
}

func TestRecallCompactedToolRejectsMissingQuery(t *testing.T) {
	tl := &RecallCompactedTool{Searcher: func(string, int) string { return "x" }}
	input, _ := json.Marshal(map[string]any{"limit": 2})
	res, err := tl.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("missing query must be a tool error")
	}
}
