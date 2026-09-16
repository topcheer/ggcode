package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCurrentTimeToolDefault(t *testing.T) {
	tool := NewCurrentTimeTool()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Current time:") || !strings.Contains(res.Content, "ISO 8601:") {
		t.Fatalf("missing expected fields in output: %s", res.Content)
	}
}

func TestCurrentTimeToolWithTimezone(t *testing.T) {
	tool := NewCurrentTimeTool()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"timezone":"Asia/Shanghai"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "CST") && !strings.Contains(res.Content, "UTC+08:00") {
		t.Fatalf("expected Asia/Shanghai zone info, got: %s", res.Content)
	}
}

func TestCurrentTimeToolInvalidTimezone(t *testing.T) {
	tool := NewCurrentTimeTool()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"timezone":"Not/AZone"}`))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result for invalid timezone, got: %s", res.Content)
	}
}
