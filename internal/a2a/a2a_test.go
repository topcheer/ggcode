package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

func TestTaskStateIsTerminal(t *testing.T) {
	terminals := []TaskState{TaskStateCompleted, TaskStateFailed, TaskStateCanceled, TaskStateRejected}
	for _, s := range terminals {
		if !s.IsTerminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}
	nonTerminals := []TaskState{TaskStateSubmitted, TaskStateWorking, TaskStateInputRequired}
	for _, s := range nonTerminals {
		if s.IsTerminal() {
			t.Errorf("expected %s to NOT be terminal", s)
		}
	}
}

func TestPartSerialization(t *testing.T) {
	p := Part{Kind: "text", Text: "hello"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var p2 Part
	if err := json.Unmarshal(data, &p2); err != nil {
		t.Fatal(err)
	}
	if p2.Kind != "text" || p2.Text != "hello" {
		t.Errorf("unexpected: %+v", p2)
	}
}

func TestArtifactSerialization(t *testing.T) {
	a := Artifact{
		ArtifactID: "art-1",
		Parts:      []Part{{Kind: "text", Text: "result"}},
		LastChunk:  true,
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var a2 Artifact
	if err := json.Unmarshal(data, &a2); err != nil {
		t.Fatal(err)
	}
	if a2.ArtifactID != "art-1" || len(a2.Parts) != 1 {
		t.Errorf("unexpected: %+v", a2)
	}
}

// ---------------------------------------------------------------------------
// Server + Client integration
// ---------------------------------------------------------------------------

func TestAgentCardEndpoint(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0, APIKey: "test-key"}, handler)

	// Start the server.
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Fetch agent card.
	resp, err := http.Get("http://127.0.0.1:" + itoa(srv.Port()) + "/.well-known/agent.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}

	if card.Name != "ggcode" {
		t.Errorf("expected name=ggcode, got %s", card.Name)
	}
	if len(card.Skills) != 6 {
		t.Errorf("expected 6 skills, got %d", len(card.Skills))
	}
	if card.Capabilities.Streaming != true {
		t.Error("expected streaming=true")
	}

	// Verify security scheme is advertised.
	if _, ok := card.SecuritySchemes["apiKey"]; !ok {
		t.Error("expected apiKey security scheme")
	}
}

func TestAuthRejectsInvalidKey(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0, APIKey: "secret"}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// No API key → 401.
	req, _ := http.NewRequest("POST", "http://127.0.0.1:"+itoa(srv.Port())+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestClientDiscover(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	client := NewClient("http://127.0.0.1:"+itoa(srv.Port()), "")
	card, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "ggcode" {
		t.Errorf("expected ggcode, got %s", card.Name)
	}
}

func TestNoAuthWhenNoKey(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0, APIKey: ""}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	// Should work without any auth.
	client := NewClient("http://127.0.0.1:"+itoa(srv.Port()), "")
	_, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

func TestGenerateInstanceID(t *testing.T) {
	id1 := GenerateInstanceID()
	id2 := GenerateInstanceID()
	if id1 == id2 {
		t.Error("expected unique IDs")
	}
	if len(id1) == 0 {
		t.Error("expected non-empty ID")
	}
}

// ---------------------------------------------------------------------------
// Handler
// ---------------------------------------------------------------------------

func TestDefaultSkills(t *testing.T) {
	skills := DefaultSkills()
	names := map[string]bool{}
	for _, s := range skills {
		names[s.ID] = true
	}
	for _, expected := range []string{SkillCodeEdit, SkillFileSearch, SkillCommandExec, SkillGitOps, SkillCodeReview, SkillFullTask} {
		if !names[expected] {
			t.Errorf("missing skill: %s", expected)
		}
	}
}

func TestSkillPermissions(t *testing.T) {
	for _, skill := range []string{SkillFileSearch, SkillGitOps, SkillCommandExec, SkillCodeEdit, SkillCodeReview, SkillFullTask} {
		perm, ok := skillPermissions[skill]
		if !ok {
			t.Errorf("no permission defined for skill: %s", skill)
			continue
		}
		if skill != SkillFullTask && len(perm.AllowedTools) == 0 {
			t.Errorf("skill %s has no allowed tools", skill)
		}
	}
}

func TestExtractText(t *testing.T) {
	msg := Message{
		Role: "user",
		Parts: []Part{
			{Kind: "text", Text: "hello"},
			{Kind: "text", Text: "world"},
		},
	}
	if got := extractText(msg); got != "hello\nworld" {
		t.Errorf("expected 'hello\\nworld', got %q", got)
	}
}

func TestPickToolForSkill(t *testing.T) {
	tests := []struct {
		skill string
		input string
		want  string
	}{
		{SkillFileSearch, "TODO", "search_files"},
		{SkillFileSearch, "*.go", "glob"},
		{SkillGitOps, "show diff", "git_diff"},
		{SkillGitOps, "recent log", "git_log"},
		{SkillGitOps, "status", "git_status"},
		{SkillCommandExec, "ls -la", "run_command"},
	}
	for _, tt := range tests {
		got := pickToolForSkill(tt.skill, tt.input)
		if got != tt.want {
			t.Errorf("pickToolForSkill(%s, %s) = %s, want %s", tt.skill, tt.input, got, tt.want)
		}
	}
}

func TestBuildToolInput(t *testing.T) {
	input := buildToolInput("search_files", "TODO")
	var m map[string]interface{}
	if err := json.Unmarshal(input, &m); err != nil {
		t.Fatal(err)
	}
	if m["pattern"] != "TODO" {
		t.Errorf("expected pattern=TODO, got %v", m["pattern"])
	}
}

// ---------------------------------------------------------------------------
// MCP Bridge
// ---------------------------------------------------------------------------

func TestMCPBridgeToolsCount(t *testing.T) {
	client := NewClient("http://localhost:9999", "")
	tools := MCPBridgeTools(client)
	if len(tools) != 4 {
		t.Fatalf("expected 4 MCP bridge tools, got %d", len(tools))
	}

	expected := map[string]bool{
		"a2a_discover":    false,
		"a2a_send_task":   false,
		"a2a_get_task":    false,
		"a2a_cancel_task": false,
	}
	for _, tool := range tools {
		if _, ok := expected[tool.Name()]; !ok {
			t.Errorf("unexpected tool: %s", tool.Name())
		}
		expected[tool.Name()] = true
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestMCPBridgeToolParameters(t *testing.T) {
	client := NewClient("http://localhost:9999", "")
	tools := MCPBridgeTools(client)
	for _, tool := range tools {
		params := tool.Parameters()
		if len(params) == 0 {
			t.Errorf("tool %s has empty parameters", tool.Name())
		}
		var schema map[string]interface{}
		if err := json.Unmarshal(params, &schema); err != nil {
			t.Errorf("tool %s has invalid parameter JSON: %v", tool.Name(), err)
		}
		if schema["type"] != "object" {
			t.Errorf("tool %s parameters should be type=object", tool.Name())
		}
	}
}

// ---------------------------------------------------------------------------
// SSE decode
// ---------------------------------------------------------------------------

func TestDecodeSSE(t *testing.T) {
	ch := make(chan JSONRPCResponse, 10)
	input := `data: {"jsonrpc":"2.0","id":"1","result":{"id":"task-1","status":{"state":"working"}}}

data: {"jsonrpc":"2.0","id":"1","result":{"id":"task-1","status":{"state":"completed"}}}

`
	go decodeSSE(newStringReader(input), ch)

	resp1 := <-ch
	if resp1.JSONRPC != "2.0" {
		t.Errorf("unexpected jsonrpc: %s", resp1.JSONRPC)
	}

	resp2 := <-ch
	resultMap, ok := resp2.Result.(map[string]interface{})
	if !ok {
		t.Fatal("expected map result")
	}
	statusMap := resultMap["status"].(map[string]interface{})
	if statusMap["state"] != "completed" {
		t.Errorf("expected completed, got %v", statusMap["state"])
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func itoa(i int) string { return fmt.Sprintf("%d", i) }

func newStringReader(s string) *stringReader { return &stringReader{s: s} }

type stringReader struct {
	s   string
	pos int
}

func (r *stringReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.s) {
		return 0, context.DeadlineExceeded
	}
	n = copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}
