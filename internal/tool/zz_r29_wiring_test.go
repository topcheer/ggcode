package tool

// r29 wiring probe: WithActor(ctx, id) -> save_memory Execute -> sidecar
// records the writer identity + result surfaces the attribution. A bare
// ctx (main agent) keeps byte-identical legacy behavior.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/util"
)

func TestSaveMemoryActorRidesCtx(t *testing.T) {
	root := t.TempDir()
	pm := memory.NewProjectAutoMemory(root)
	tool := NewSaveMemoryTool(nil, pm)
	dir := root + "/.ggcode/memory"

	input, _ := json.Marshal(map[string]string{"key": "sub-written", "content": "body", "scope": "project"})
	res, err := tool.Execute(util.WithActor(context.Background(), "agent-42"), input)
	if err != nil || res.IsError {
		t.Fatalf("execute: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, "agent-42") {
		t.Fatalf("result missing attribution: %q", res.Content)
	}
	raw, _ := os.ReadFile(dir + "/.usage.json")
	if !strings.Contains(string(raw), `"actor":"agent-42"`) {
		t.Fatalf("sidecar missing actor: %s", raw)
	}
}

func TestSaveMemoryLegacyCtxNoActor(t *testing.T) {
	root := t.TempDir()
	pm := memory.NewProjectAutoMemory(root)
	tool := NewSaveMemoryTool(nil, pm)
	dir := root + "/.ggcode/memory"

	input, _ := json.Marshal(map[string]string{"key": "main-written", "content": "body", "scope": "project"})
	res, err := tool.Execute(context.Background(), input)
	if err != nil || res.IsError {
		t.Fatalf("execute: err=%v res=%+v", err, res)
	}
	if strings.Contains(res.Content, "provenance") {
		t.Fatalf("legacy ctx must not add attribution note: %q", res.Content)
	}
	raw, _ := os.ReadFile(dir + "/.usage.json")
	if strings.Contains(string(raw), `"actor"`) {
		t.Fatalf("legacy ctx must not write actor field: %s", raw)
	}
}
