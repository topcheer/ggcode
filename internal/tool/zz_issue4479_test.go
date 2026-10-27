package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zz_issue4479_test.go pins the two #4479 contracts:
//
//	Bug1 (medium): two cloned KnowledgeGraphTool instances (one per agent)
//	               writing the same workspace graph must not silently drop
//	               each other's nodes/edges (lost update).
//	Bug2 (low):    explicit IDs longer than 80 bytes are truncated on a rune
//	               boundary, never mid-rune (invalid UTF-8).
func TestKnowledgeGraphConcurrentInstancesMergeOnSave(t *testing.T) {
	dir := t.TempDir()

	// Two independently-cloned instances, as Registry.Clone hands one to
	// each agent. Both load the same (empty) file before either writes.
	agentA := &KnowledgeGraphTool{WorkingDir: dir}
	agentB := &KnowledgeGraphTool{WorkingDir: dir}

	ctx := context.Background()

	mustExec := func(tool *KnowledgeGraphTool, input string) string {
		res, err := tool.Execute(ctx, json.RawMessage(input))
		if err != nil {
			t.Fatalf("execute %s: %v", input, err)
		}
		if res.IsError {
			t.Fatalf("execute %s failed: %s", input, res.Content)
		}
		return res.Content
	}

	// A adds its node first.
	mustExec(agentA, `{"action":"add","id":"node-a","type":"note","title":"A"}`)
	// B has already loaded (Execute above for B? No - first B write now loads
	// the file WITH node-a? It must: B's load happens on first Execute, which
	// is after A's save, so B starts from A's state. The lost-update window is
	// the reverse order, covered next.)
	mustExec(agentB, `{"action":"add","id":"node-b","type":"note","title":"B"}`)

	// Window: both instances now hold loaded caches. A writes again - with
	// the old code this overwrote the file WITHOUT node-b.
	mustExec(agentA, `{"action":"add","id":"node-a2","type":"note","title":"A2"}`)

	// Fresh third instance could read the merged file, but we assert on the
	// file itself: query matches title/content/tags, not IDs.
	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "knowledge-graph.json"))
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	for _, want := range []string{"node-a", "node-b", "node-a2"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("lost update: %q missing after A/B interleaved writes; content:\n%s", want, data)
		}
	}
}

func TestKnowledgeGraphConcurrentEdgesMerged(t *testing.T) {
	dir := t.TempDir()
	a := &KnowledgeGraphTool{WorkingDir: dir}
	b := &KnowledgeGraphTool{WorkingDir: dir}
	ctx := context.Background()

	for _, in := range []string{
		`{"action":"add","id":"d1","type":"decision","title":"D1"}`,
		`{"action":"add","id":"d2","type":"decision","title":"D2"}`,
	} {
		if res, err := a.Execute(ctx, json.RawMessage(in)); err != nil || res.IsError {
			t.Fatalf("seed %s: err=%v content=%s", in, err, res.Content)
		}
	}
	// Both instances have loaded caches now (b loaded during seed). Each
	// links a distinct edge, A last - must not erase B's edge.
	if res, err := b.Execute(ctx, json.RawMessage(`{"action":"link","id":"d1","to":"d2","type":"relates-to"}`)); err != nil || res.IsError {
		t.Fatalf("link B: err=%v content=%s", err, res.Content)
	}
	if res, err := a.Execute(ctx, json.RawMessage(`{"action":"add","id":"p1","type":"pattern","title":"P1"}`)); err != nil || res.IsError {
		t.Fatalf("add A: err=%v content=%s", err, res.Content)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "knowledge-graph.json"))
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var store struct {
		Edges []struct {
			From string `json:"from"`
			To   string `json:"to"`
			Type string `json:"type"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, e := range store.Edges {
		if e.From == "d1" && e.To == "d2" && e.Type == "relates-to" {
			found = true
		}
	}
	if !found {
		t.Fatalf("B's edge d1->d2 lost after A's later save; edges: %+v", store.Edges)
	}
}

func TestKnowledgeGraphExplicitCJKIDTruncatedRuneSafe(t *testing.T) {
	dir := t.TempDir()
	kg := &KnowledgeGraphTool{WorkingDir: dir}
	ctx := context.Background()

	// 40 CJK chars = 120 bytes > 80: the cut must land on a rune boundary.
	longID := strings.Repeat("策", 40)
	res, err := kg.Execute(ctx, json.RawMessage(kg4479Marshal(t, struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Type   string `json:"type"`
		Title  string `json:"title"`
	}{"add", longID, "note", "CJK id"})))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("add failed: %s", res.Content)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "knowledge-graph.json"))
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	var store kgStore
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(store.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(store.Nodes))
	}
	for id := range store.Nodes {
		if len(id) > 80 {
			t.Fatalf("id not truncated: %d bytes", len(id))
		}
		if !utf8ValidString(id) {
			t.Fatalf("id is not valid UTF-8 after truncation: %q", id)
		}
	}
}

func kg4479Marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			// A literal replacement char could be legitimate input, but our
			// test input ("策" repeated) contains none, so any U+FFFD here
			// means the marshal-rewrote-invalid-bytes path fired.
			return false
		}
	}
	return true
}
