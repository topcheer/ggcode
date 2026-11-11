package agentruntime

import (
	"encoding/json"
	"sort"
	"testing"

	"strings"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestIssue5396RegisteredToolSchemasAreValidJSON sweeps every tool registered
// by RegisterCronTools and asserts Parameters() returns valid JSON. #5396
// shipped a cron_create schema whose properties object was never closed
// (a property deletion ate the closing brace); the Responses endpoint embeds
// the raw schema unvalidated and every request failed to encode. Sweeping at
// the registry level stops any future same-class regression from shipping.
func TestIssue5396RegisteredToolSchemasAreValidJSON(t *testing.T) {
	registry := tool.NewRegistry()
	scheduler := NewSessionCronScheduler("test-issue-5396", t.TempDir(), nil)
	defer scheduler.Shutdown()
	RegisterCronTools(registry, scheduler)

	tools := registry.List()
	if len(tools) == 0 {
		t.Fatal("RegisterCronTools registered no tools")
	}
	for _, tl := range tools {
		raw := tl.Parameters()
		if len(raw) == 0 {
			continue
		}
		if !json.Valid(raw) {
			t.Errorf("%s Parameters() is not valid JSON", tl.Name())
		}
	}
}

// TestIssue5396AssembledRegistrySchemasAreValidJSON sweeps the registry the
// way production assembles it (RegisterBuiltinTools + RegisterCronTools, the
// daemon.go:573 ordering) and asserts every registered tool's Parameters()
// is valid JSON. The internal/tool builtin-only guard does NOT cover the
// cron family - verified empirically: it passes at the pre-#5396 parent
// edc395a4c while cron_create was still broken - so this is the class-level
// net for the tool family that actually caused #5396, plus the whole
// assembled registry, at the layer the runtime consumes.
func TestIssue5396AssembledRegistrySchemasAreValidJSON(t *testing.T) {
	registry := tool.NewRegistry()
	scheduler := NewSessionCronScheduler("test-issue-5396-asm", t.TempDir(), nil)
	defer scheduler.Shutdown()
	if err := tool.RegisterBuiltinTools(registry, nil, t.TempDir(), nil, nil); err != nil {
		t.Fatalf("RegisterBuiltinTools: %v", err)
	}
	RegisterCronTools(registry, scheduler)

	tools := registry.List()
	if len(tools) < 27 {
		t.Fatalf("expected assembled registry with builtin+cron tools, got %d", len(tools))
	}
	cronFamily := 0
	for _, tl := range tools {
		if strings.HasPrefix(tl.Name(), "cron_") {
			cronFamily++
		}
		raw := tl.Parameters()
		if len(raw) == 0 {
			continue
		}
		if !json.Valid(raw) {
			t.Errorf("%s Parameters() is not valid JSON", tl.Name())
		}
	}
	if cronFamily != 7 {
		t.Fatalf("cron family must be inside the assembled-registry net, got %d cron tools", cronFamily)
	}
}

// TestIssue5396CronCreateSchemaContract pins the cron_create schema content
// contract (property keys + required list) that bc404fff3 silently damaged:
// the far_horizon_ack deletion ate the properties-closing brace, so models
// lost the required-field constraints and provider endpoints degraded
// silently. Rows adopted from the unlanded #5397 test.
func TestIssue5396CronCreateSchemaContract(t *testing.T) {
	registry := tool.NewRegistry()
	scheduler := NewSessionCronScheduler("test-issue-5396-contract", t.TempDir(), nil)
	defer scheduler.Shutdown()
	RegisterCronTools(registry, scheduler)

	tl, ok := registry.Get("cron_create")
	if !ok {
		t.Fatal("cron_create not registered")
	}
	raw := tl.Parameters()
	if !json.Valid(raw) {
		t.Fatalf("cron_create Parameters() is not valid JSON:\n%s", string(raw))
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("cron_create schema unmarshal failed: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties must be an object, got %T", schema["properties"])
	}
	for _, key := range []string{"cron", "prompt", "description", "recurring", "one_shot", "far", "queue_if_busy"} {
		if _, ok := props[key]; !ok {
			t.Errorf("cron_create schema missing property %q", key)
		}
	}
	req, ok := schema["required"].([]any)
	if !ok || len(req) == 0 {
		t.Fatalf("required must be a non-empty array, got %T", schema["required"])
	}
	reqKeys := make(map[string]bool, len(req))
	for _, r := range req {
		if s, ok := r.(string); ok {
			reqKeys[s] = true
		}
	}
	for _, key := range []string{"cron", "prompt", "description"} {
		if !reqKeys[key] {
			t.Errorf("cron_create required must contain %q, got %v", key, req)
		}
	}
}

// TestIssue5396CronToolsDescriptionsAndNames pins the cron tool family shape
// so accidental property-block deletions that also drop Name/Description
// surface loudly in review.
func TestIssue5396CronToolsDescriptionsAndNames(t *testing.T) {
	registry := tool.NewRegistry()
	scheduler := NewSessionCronScheduler("test-issue-5396-shape", t.TempDir(), nil)
	defer scheduler.Shutdown()
	RegisterCronTools(registry, scheduler)

	want := []string{"cron_create", "cron_delete", "cron_get", "cron_list", "cron_pause", "cron_resume", "cron_update"}
	got := make([]string, 0, len(want))
	for _, tl := range registry.List() {
		got = append(got, tl.Name())
	}
	sort.Strings(got)
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("cron tool names = %v, want %v", got, want)
		}
	}
	for _, tl := range registry.List() {
		if tl.Description() == "" {
			t.Errorf("tool %s has empty Description()", tl.Name())
		}
	}
}
