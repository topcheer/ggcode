package agentruntime

import (
	"encoding/json"
	"sort"
	"testing"

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
