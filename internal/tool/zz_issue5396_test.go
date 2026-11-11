package tool

import (
	"encoding/json"
	"testing"
)

// TestIssue5396CronCreateSchemaValid pins the cron_create Parameters() JSON
// being valid JSON Schema. bc404fff3 dropped the far_horizon_ack property
// together with the properties closing brace, leaving the raw string as
// invalid JSON (10 open braces vs 9 close). The OpenAI Responses endpoint
// embeds the raw schema without validation, so every request failed to
// encode; other providers silently degraded to an empty schema.
func TestIssue5396CronCreateSchemaValid(t *testing.T) {
	raw := CronCreateTool{}.Parameters()
	if len(raw) == 0 {
		t.Fatal("cron_create Parameters() returned empty schema")
	}
	if !json.Valid(raw) {
		t.Fatalf("cron_create Parameters() is not valid JSON (braces open=%d close=%d)",
			countByte(raw, '{'), countByte(raw, '}'))
	}

	var schema struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("cron_create Parameters() failed to unmarshal: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("schema type = %q, want object", schema.Type)
	}
	for _, prop := range []string{"cron", "prompt", "recurring", "one_shot", "far", "queue_if_busy", "durable", "description"} {
		if _, ok := schema.Properties[prop]; !ok {
			t.Errorf("cron_create schema missing property %q (have %v)", prop, keysOf(schema.Properties))
		}
	}
	if len(schema.Required) != 3 || schema.Required[0] != "cron" || schema.Required[1] != "prompt" || schema.Required[2] != "description" {
		t.Errorf("cron_create required = %v, want [cron prompt description]", schema.Required)
	}
}

func countByte(b []byte, c byte) int {
	n := 0
	for _, x := range b {
		if x == c {
			n++
		}
	}
	return n
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
