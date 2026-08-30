package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func newKGTool(t *testing.T) (*KnowledgeGraphTool, string) {
	t.Helper()
	dir := t.TempDir()
	tool := &KnowledgeGraphTool{WorkingDir: dir}
	return tool, dir
}

func TestKGAddAndList(t *testing.T) {
	tool, _ := newKGTool(t)

	// Add a decision node
	r, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"add","type":"decision","title":"Use Go for backend","content":"Go for performance","status":"accepted","tags":["architecture"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError {
		t.Fatalf("add failed: %s", r.Content)
	}

	// Add a pattern node with auto-generated id
	r, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"add","type":"pattern","title":"Repository Pattern","content":"Use repos for data access"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError {
		t.Fatalf("add pattern failed: %s", r.Content)
	}

	// List should show both
	r, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError {
		t.Fatalf("list failed: %s", r.Content)
	}
	if !contains(r.Content, "Use Go for backend") {
		t.Errorf("list missing decision: %s", r.Content)
	}
	if !contains(r.Content, "Repository Pattern") {
		t.Errorf("list missing pattern: %s", r.Content)
	}
}

func TestKGAddUpdate(t *testing.T) {
	tool, _ := newKGTool(t)

	// Create
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"arch-1","type":"entity","title":"Auth Module","content":"Handles login"}`))
	if err != nil {
		t.Fatal(err)
	}

	// Update same id
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"arch-1","type":"entity","title":"Auth Module v2","content":"Handles login + OAuth"}`))
	if err != nil {
		t.Fatal(err)
	}

	// Query should show updated title
	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"query","query":"v2"}`))
	if !contains(r.Content, "Auth Module v2") {
		t.Errorf("update not reflected: %s", r.Content)
	}

	// Should only be 1 node
	r, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"stats"}`))
	if !contains(r.Content, "1/") {
		t.Errorf("expected 1 node, got: %s", r.Content)
	}
}

func TestKGLinkAndTrace(t *testing.T) {
	tool, _ := newKGTool(t)

	// Add 3 nodes
	for _, p := range []struct{ id, title, typ string }{
		{"auth", "Auth Module", "entity"},
		{"oauth", "OAuth Provider", "entity"},
		{"session", "Session Manager", "entity"},
	} {
		_, err := tool.Execute(context.Background(), mustMarshal(map[string]interface{}{
			"action": "add", "id": p.id, "type": p.typ, "title": p.title,
		}))
		if err != nil {
			t.Fatal(err)
		}
	}

	// auth depends-on oauth
	r, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"link","id":"auth","to":"oauth","type":"depends-on"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError {
		t.Fatalf("link failed: %s", r.Content)
	}

	// oauth implements session -- wait, this is backwards for the trace.
	// oauth relates-to session
	_, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"link","id":"oauth","to":"session","type":"relates-to"}`))

	// Trace from auth
	r, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"trace","id":"auth"}`))
	if !contains(r.Content, "depends-on") {
		t.Errorf("trace missing depends-on edge: %s", r.Content)
	}
	if !contains(r.Content, "OAuth Provider") {
		t.Errorf("trace missing target: %s", r.Content)
	}
	if !contains(r.Content, "relates-to") {
		t.Errorf("trace missing 2nd hop: %s", r.Content)
	}
}

func TestKGLinkDuplicate(t *testing.T) {
	tool, _ := newKGTool(t)
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"a","type":"entity","title":"A"}`))
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"b","type":"entity","title":"B"}`))

	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"link","id":"a","to":"b","type":"relates-to"}`))
	if contains(r.Content, "already exists") {
		t.Fatal("first link should not be duplicate")
	}

	// Link again
	r, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"link","id":"a","to":"b","type":"relates-to"}`))
	if !contains(r.Content, "already exists") {
		t.Errorf("expected duplicate edge: %s", r.Content)
	}
}

func TestKGDeleteRemovesEdges(t *testing.T) {
	tool, _ := newKGTool(t)
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"a","type":"entity","title":"A"}`))
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"b","type":"entity","title":"B"}`))
	tool.Execute(context.Background(), json.RawMessage(`{"action":"link","id":"a","to":"b","type":"relates-to"}`))

	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"delete","id":"b"}`))
	if r.IsError {
		t.Fatalf("delete failed: %s", r.Content)
	}
	if !contains(r.Content, "1 edges") {
		t.Errorf("expected 1 edge removed: %s", r.Content)
	}
}

func TestKGQueryByType(t *testing.T) {
	tool, _ := newKGTool(t)
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","type":"decision","title":"Decision One"}`))
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","type":"pattern","title":"Pattern One"}`))

	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"query","type":"decision"}`))
	if !contains(r.Content, "Decision One") {
		t.Errorf("missing result: %s", r.Content)
	}
	if contains(r.Content, "Pattern One") {
		t.Errorf("type filter not applied: %s", r.Content)
	}
}

func TestKGStats(t *testing.T) {
	tool, _ := newKGTool(t)
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","type":"decision","title":"D1","status":"accepted"}`))
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","type":"note","title":"N1"}`))

	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"stats"}`))
	if !contains(r.Content, "2/") {
		t.Errorf("expected 2 nodes: %s", r.Content)
	}
	if !contains(r.Content, "decision: 1") {
		t.Errorf("missing decision count: %s", r.Content)
	}
	if !contains(r.Content, "note: 1") {
		t.Errorf("missing note count: %s", r.Content)
	}
}

func TestKGInvalidNodeType(t *testing.T) {
	tool, _ := newKGTool(t)
	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"add","type":"invalid","title":"Test"}`))
	if !r.IsError {
		t.Error("expected error for invalid node type")
	}
}

func TestKGInvalidEdgeType(t *testing.T) {
	tool, _ := newKGTool(t)
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"a","type":"entity","title":"A"}`))
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"b","type":"entity","title":"B"}`))

	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"link","id":"a","to":"b","type":"invalid-edge"}`))
	if !r.IsError {
		t.Error("expected error for invalid edge type")
	}
}

func TestKGEmptyList(t *testing.T) {
	tool, _ := newKGTool(t)
	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if !contains(r.Content, "empty") {
		t.Errorf("expected empty message: %s", r.Content)
	}
}

func TestKGEmptyQuery(t *testing.T) {
	tool, _ := newKGTool(t)
	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"query","query":"nothing"}`))
	if !contains(r.Content, "No matching") {
		t.Errorf("expected no results: %s", r.Content)
	}
}

func TestKGPersistenceAcrossInstances(t *testing.T) {
	dir := t.TempDir()

	tool1 := &KnowledgeGraphTool{WorkingDir: dir}
	_, err := tool1.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"test","type":"decision","title":"Persisted Decision"}`))
	if err != nil {
		t.Fatal(err)
	}

	// New tool instance, same directory
	tool2 := &KnowledgeGraphTool{WorkingDir: dir}
	r, err := tool2.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(r.Content, "Persisted Decision") {
		t.Errorf("persistence failed - node not found: %s", r.Content)
	}

	// Verify file exists
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", kgFileName)); os.IsNotExist(err) {
		t.Error("knowledge-graph.json file not created")
	}
}

func TestKGAddMissingTitle(t *testing.T) {
	tool, _ := newKGTool(t)
	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"add","type":"note"}`))
	if !r.IsError {
		t.Error("expected error for missing title")
	}
}

func TestKGTraceNoOutgoing(t *testing.T) {
	tool, _ := newKGTool(t)
	tool.Execute(context.Background(), json.RawMessage(`{"action":"add","id":"solo","type":"entity","title":"Solo Entity"}`))

	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"trace","id":"solo"}`))
	if !contains(r.Content, "no outgoing") {
		t.Errorf("expected no relationships: %s", r.Content)
	}
}

func TestKGSlugify(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Use Go for Backend", "use-go-for-backend"},
		{"Test_Node", "test-node"},
		{"Path/To/Module", "path-to-module"},
		{"Double  Space", "double-space"},
	}
	for _, c := range cases {
		got := slugify(c.input)
		if got != c.expected {
			t.Errorf("slugify(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func mustMarshal(m map[string]interface{}) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

// #1327: update branch assigned Type unconditionally while nt defaults
// to "note" - a partial update (id+title, no type) silently reclassified
// decision/entity nodes as note and the next save persisted it.
func TestKnowledgeGraphPartialUpdatePreservesType(t *testing.T) {
	tool, _ := newKGTool(t)

	add := `{"action":"add","id":"d1","title":"Use context pinning","type":"decision","content":"decided"}`
	if r, err := tool.Execute(context.Background(), json.RawMessage(add)); err != nil || r.IsError {
		t.Fatalf("add: %v %s", err, r.Content)
	}
	// Partial update carrying id+title+status but NO type.
	upd := `{"action":"add","id":"d1","title":"Use context pinning v2","status":"superseded"}`
	if r, err := tool.Execute(context.Background(), json.RawMessage(upd)); err != nil || r.IsError {
		t.Fatalf("update: %v %s", err, r.Content)
	}
	r, _ := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if !contains(r.Content, "[decision] (1)") {
		t.Errorf("partial update demoted node type: %s", r.Content)
	}

	// Explicit type change still works.
	chg := `{"action":"add","id":"d1","title":"Use context pinning v2","type":"note"}`
	if r, err := tool.Execute(context.Background(), json.RawMessage(chg)); err != nil || r.IsError {
		t.Fatalf("explicit change: %v %s", err, r.Content)
	}
	r, _ = tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if !contains(r.Content, "[note] (1)") {
		t.Errorf("explicit type change not applied: %s", r.Content)
	}
}
