package tool

import (
	"encoding/json"
	"testing"
)

// Pin for issue #5396: CronCreateTool.Parameters() must return VALID JSON.
// The bc404fff3 far-ack cleanup dropped the properties-closing brace,
// breaking Responses-endpoint requests entirely and silently disabling
// schema validation elsewhere.
func TestCronCreateSchemaIsValidJSON(t *testing.T) {
	raw := CronCreateTool{}.Parameters()
	if len(raw) == 0 {
		t.Fatal("Parameters() returned empty schema")
	}
	if !json.Valid(raw) {
		t.Fatalf("CronCreateTool.Parameters() is not valid JSON (issue #5396):\n%s", string(raw))
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema unmarshal failed: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties must be an object, got %T", schema["properties"])
	}
	for _, key := range []string{"cron", "prompt", "description", "far", "one_shot", "queue_if_busy"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema missing property %q", key)
		}
	}
	req, ok := schema["required"].([]any)
	if !ok || len(req) == 0 {
		t.Fatalf("required must be a non-empty array, got %T", schema["required"])
	}
}
