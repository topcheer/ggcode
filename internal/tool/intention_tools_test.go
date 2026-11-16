package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/intentions"
)

func newIntentionTool(t *testing.T) (IntentionTool, *intentions.Store, *intentions.Engine) {
	t.Helper()
	dir := t.TempDir()
	store := intentions.NewStore(dir+"/intentions.json", "sess-test")
	engine := intentions.NewEngine(store, dir, func(string, bool) {})
	return IntentionTool{Store: store, Engine: engine}, store, engine
}

func TestIntentionToolPutRequiresUtteranceExcerpt(t *testing.T) {
	tool, _, _ := newIntentionTool(t)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{
		"action": "put",
		"statement": "continue release",
		"cue_family": "event",
		"cue_pattern": "CI green",
		"origin_excerpt": ""
	}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "origin_excerpt") {
		t.Fatalf("expected origin_excerpt validation error, got: %s", res.Content)
	}
}

func TestIntentionToolLifecycleRoundTrip(t *testing.T) {
	tool, store, engine := newIntentionTool(t)

	res, _ := tool.Execute(context.Background(), json.RawMessage(`{
		"action": "put",
		"statement": "continue the release for #5257",
		"cue_family": "event",
		"cue_pattern": "CI green on #5257",
		"origin_excerpt": "when CI is green on #5257, continue the release",
		"origin_session": "sess-42"
	}`))
	if res.IsError {
		t.Fatalf("put failed: %s", res.Content)
	}
	id := extractID(t, res.Content)

	// list shows it
	lres, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if lres.IsError || !strings.Contains(lres.Content, "continue the release for #5257") {
		t.Fatalf("list missing intention: %s", lres.Content)
	}

	// observe matching event fires it
	ores, _ := tool.Execute(context.Background(), json.RawMessage(`{
		"action": "observe",
		"kind": "tool_result",
		"text": "checks: CI green on #5257"
	}`))
	if ores.IsError {
		t.Fatalf("observe failed: %s", ores.Content)
	}
	in, _ := store.FindByID(id)
	if in.State != intentions.StateFired {
		t.Fatalf("state = %s, want fired", in.State)
	}
	_ = engine

	// realize
	rres, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"realize","id":"`+id+`"}`))
	if rres.IsError {
		t.Fatalf("realize failed: %s", rres.Content)
	}
	in, _ = store.FindByID(id)
	if in.State != intentions.StateRealized {
		t.Fatalf("state = %s, want realized", in.State)
	}
}

func TestIntentionToolRetireAndErrors(t *testing.T) {
	tool, _, _ := newIntentionTool(t)

	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"retire","id":""}`))
	if !res.IsError {
		t.Fatal("retire without id should error")
	}
	res, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"bogus"}`))
	if !res.IsError {
		t.Fatal("unknown action should error")
	}
	// nil-store tool: every action must error, never panic
	var nilTool IntentionTool
	res, err := nilTool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("nil store should yield error result")
	}
}

func extractID(t *testing.T, content string) string {
	t.Helper()
	const marker = "id="
	i := strings.Index(content, marker)
	if i < 0 {
		t.Fatalf("no id in put result: %s", content)
	}
	rest := content[i+len(marker):]
	if j := strings.IndexAny(rest, " \n"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}
