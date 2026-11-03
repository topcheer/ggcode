package tool

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func kgTestToolWithFanIn() *KnowledgeGraphTool {
	dir, _ := os.MkdirTemp("", "kg4904-*")
	kg := &KnowledgeGraphTool{WorkingDir: dir}
	add := func(title string) {
		args, _ := json.Marshal(map[string]any{
			"action": "add", "title": title, "type": "note",
		})
		kg.Execute(context.Background(), args)
	}
	add("A")
	add("B")
	add("C")
	link := func(from, to string) {
		args, _ := json.Marshal(map[string]any{
			"action": "link", "id": from, "to": to, "type": "relates-to",
		})
		kg.Execute(context.Background(), args)
	}
	link("a", "c")
	link("b", "c")
	link("a", "b")
	return kg
}

func TestTraceKeepsFanInEdges(t *testing.T) {
	tk := kgTestToolWithFanIn()
	args, _ := json.Marshal(map[string]any{"action": "trace", "id": "a"})
	res, err := tk.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("trace: %v", err)
	}
	if res.IsError {
		t.Fatalf("trace errored: %s", res.Content)
	}
	gotC := strings.Count(res.Content, "--[relates-to]--> [note] C")
	if gotC < 2 {
		t.Fatalf("#4904 regression: fan-in edges to c under-reported, got %d 'relates-to--> c' lines, want >= 2:\n%s", gotC, res.Content)
	}
}

func TestTraceStillDedupesCycles(t *testing.T) {
	kg := &KnowledgeGraphTool{WorkingDir: t.TempDir()}
	add := func(title string) {
		args, _ := json.Marshal(map[string]any{
			"action": "add", "title": title, "type": "note",
		})
		kg.Execute(context.Background(), args)
	}
	add("X")
	add("Y")
	link := func(from, to string) {
		args, _ := json.Marshal(map[string]any{
			"action": "link", "id": from, "to": to, "type": "depends-on",
		})
		kg.Execute(context.Background(), args)
	}
	link("x", "y")
	link("y", "x")
	args, _ := json.Marshal(map[string]any{"action": "trace", "id": "x"})
	res, err := kg.Execute(context.Background(), args)
	if err != nil || res.IsError {
		t.Fatalf("trace cycle: err=%v content=%s", err, res.Content)
	}

	if strings.Count(res.Content, "--[depends-on]-->") > 2 {
		t.Fatalf("cycle not deduped, output grew:\n%s", res.Content)
	}
}

func TestTraceAsOfUnaffected(t *testing.T) {
	tk := kgTestToolWithFanIn()
	old := time.Now().Add(-time.Hour).Format(time.RFC3339)
	args, _ := json.Marshal(map[string]any{"action": "trace", "id": "a", "as_of": old})
	res, err := tk.Execute(context.Background(), args)
	if err != nil || res.IsError {
		t.Fatalf("trace as_of: err=%v content=%s", err, res.Content)
	}
	if strings.Contains(res.Content, "--[relates-to]-->") {
		t.Fatalf("edges created now must be hidden at past as_of:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "hidden") && !strings.Contains(res.Content, "Hidden") {
		t.Fatalf("hidden count should surface for past as_of:\n%s", res.Content)
	}
}
