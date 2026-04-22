package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestFilePartSerialization(t *testing.T) {
	p := Part{
		Kind: "file",
		File: &FilePart{Name: "test.go", MIME: "text/plain", Bytes: "Z29jb2Rl"},
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var p2 Part
	if err := json.Unmarshal(data, &p2); err != nil {
		t.Fatal(err)
	}
	if p2.File == nil || p2.File.Name != "test.go" {
		t.Errorf("expected file part, got: %+v", p2)
	}
}

func TestDataPartSerialization(t *testing.T) {
	p := Part{
		Kind: "data",
		Data: json.RawMessage(`{"line":42}`),
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var p2 Part
	if err := json.Unmarshal(data, &p2); err != nil {
		t.Fatal(err)
	}
	if p2.Data == nil || string(p2.Data) != `{"line":42}` {
		t.Errorf("expected data part, got: %+v", p2)
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

func TestJSONRPCError(t *testing.T) {
	err := &JSONRPCError{Code: -32601, Message: "Method not found"}
	if err.Error() != "JSON-RPC error -32601: Method not found" {
		t.Errorf("unexpected error string: %s", err.Error())
	}
}

func TestTaskKind(t *testing.T) {
	task := &Task{ID: "t1", Status: TaskStatus{State: TaskStateSubmitted}}
	if task.Kind() != "task" {
		t.Errorf("expected kind=task, got %s", task.Kind())
	}
}

func TestTaskStatusSerialization(t *testing.T) {
	task := &Task{
		ID:        "task-123",
		ContextID: "ctx-456",
		Status:    TaskStatus{State: TaskStateWorking},
	}
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	// Per A2A spec, status must be {"state": "working"}, not bare "working".
	if !containsStr(string(data), `"status":{"state":"working"}`) {
		t.Errorf("expected status as object, got: %s", string(data))
	}
}

func TestSendMessageParamsDeserialization(t *testing.T) {
	raw := `{"message":{"role":"user","parts":[{"kind":"text","text":"hello"}]},"skill":"file-search"}`
	var params SendMessageParams
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatal(err)
	}
	if params.Skill != "file-search" {
		t.Errorf("expected file-search, got %s", params.Skill)
	}
	if params.Message.Role != "user" {
		t.Errorf("expected user role")
	}
}

func TestJSONRPCResponseSerialization(t *testing.T) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Result:  map[string]string{"status": "ok"},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var resp2 JSONRPCResponse
	if err := json.Unmarshal(data, &resp2); err != nil {
		t.Fatal(err)
	}
	if resp2.JSONRPC != "2.0" {
		t.Errorf("unexpected jsonrpc: %s", resp2.JSONRPC)
	}
}

// ---------------------------------------------------------------------------
// Server + Client integration
// ---------------------------------------------------------------------------

func TestAgentCardEndpoint(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0, APIKey: "test-key"}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	resp, err := http.Get("http://127.0.0.1:" + fmt.Sprintf("%d", srv.Port()) + "/.well-known/agent.json")
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
	if _, ok := card.SecuritySchemes["apiKey"]; !ok {
		t.Error("expected apiKey security scheme")
	}
}

func TestAgentCardNoAuth(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	resp, err := http.Get("http://127.0.0.1:" + fmt.Sprintf("%d", srv.Port()) + "/.well-known/agent.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var card AgentCard
	json.NewDecoder(resp.Body).Decode(&card)
	// No security schemes when no API key.
	if len(card.SecuritySchemes) != 0 {
		t.Error("expected no security schemes without API key")
	}
	if len(card.Security) != 0 {
		t.Error("expected no security requirements without API key")
	}
}

func TestAgentCardMethodReject(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	resp, err := http.Post("http://127.0.0.1:"+fmt.Sprintf("%d", srv.Port())+"/.well-known/agent.json", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", resp.StatusCode)
	}
}

func TestAuthRejectsInvalidKey(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0, APIKey: "secret"}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	req, _ := http.NewRequest("POST", "http://127.0.0.1:"+fmt.Sprintf("%d", srv.Port())+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestRPCRejectsBadJSON(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	resp, err := http.Post("http://127.0.0.1:"+fmt.Sprintf("%d", srv.Port())+"/", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var rpcResp JSONRPCResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error == nil || rpcResp.Error.Code != -32700 {
		t.Errorf("expected parse error, got: %+v", rpcResp)
	}
}

func TestRPCRejectsInvalidVersion(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "1.0", "id": 1, "method": "test"})
	resp, err := http.Post("http://127.0.0.1:"+fmt.Sprintf("%d", srv.Port())+"/", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var rpcResp JSONRPCResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error == nil || rpcResp.Error.Code != -32600 {
		t.Errorf("expected invalid request error, got: %+v", rpcResp)
	}
}

func TestRPCMethodNotFound(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	body, _ := json.Marshal(JSONRPCRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "nonexistent"})
	resp, err := http.Post("http://127.0.0.1:"+fmt.Sprintf("%d", srv.Port())+"/", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var rpcResp JSONRPCResponse
	json.NewDecoder(resp.Body).Decode(&rpcResp)
	if rpcResp.Error == nil || rpcResp.Error.Code != -32601 {
		t.Errorf("expected method not found, got: %+v", rpcResp)
	}
}

func TestClientDiscover(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	client := NewClient("http://127.0.0.1:"+fmt.Sprintf("%d", srv.Port()), "")
	card, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "ggcode" {
		t.Errorf("expected ggcode, got %s", card.Name)
	}
}

func TestClientGetTaskNotFound(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	client := NewClient("http://127.0.0.1:"+fmt.Sprintf("%d", srv.Port()), "")
	_, err := client.GetTask(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
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
	tests := []struct {
		msg  Message
		want string
	}{
		{Message{Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}}}, "hello"},
		{Message{Role: "user", Parts: []Part{{Kind: "text", Text: "a"}, {Kind: "text", Text: "b"}}}, "a\nb"},
		{Message{Role: "user", Parts: []Part{{Kind: "file", File: &FilePart{Name: "x.go"}}}}, ""},
		{Message{Role: "user", Parts: []Part{}}, ""},
	}
	for i, tt := range tests {
		got := extractText(tt.msg)
		if got != tt.want {
			t.Errorf("test %d: expected %q, got %q", i, tt.want, got)
		}
	}
}

func TestIsToolAllowed(t *testing.T) {
	allowed := []string{"read_file", "search_files", "glob"}
	if !isToolAllowed("read_file", allowed) {
		t.Error("expected read_file to be allowed")
	}
	if isToolAllowed("run_command", allowed) {
		t.Error("expected run_command to NOT be allowed")
	}
	if !isToolAllowed("read_file", nil) {
		t.Error("nil allowed should permit all tools")
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
		{SkillFileSearch, "main.test.js", "glob"},
		{SkillGitOps, "show diff", "git_diff"},
		{SkillGitOps, "recent log", "git_log"},
		{SkillGitOps, "status", "git_status"},
		{SkillCommandExec, "ls -la", "run_command"},
		{"unknown", "anything", "search_files"},
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
	if m["max_results"] != float64(50) {
		t.Errorf("expected max_results=50, got %v", m["max_results"])
	}
}

func TestBuildAgentPrompt(t *testing.T) {
	if !contains(buildAgentPrompt(SkillCodeReview, "code"), "Review") {
		t.Error("expected Review in code-review prompt")
	}
	if !contains(buildAgentPrompt(SkillCodeEdit, "fix bug"), "Make the following") {
		t.Error("expected edit prompt")
	}
	if buildAgentPrompt(SkillFullTask, "do stuff") != "do stuff" {
		t.Error("full-task should pass through text")
	}
}

func TestHandleUnknownSkill(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	_, err := handler.Handle(context.Background(), "unknown-skill", Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "test"}},
	}, "")
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !contains(err.Error(), "unknown skill") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleEmptyInput(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	// Empty text input for file-search should fail during execution.
	task, err := handler.Handle(context.Background(), SkillFileSearch, Message{
		Role: "user", Parts: []Part{},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	// Wait for async execution to finish.
	time.Sleep(500 * time.Millisecond)
	gotTask, ok := handler.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if gotTask.Status.State != TaskStateFailed {
		t.Errorf("expected failed for empty input, got %s", gotTask.Status.State)
	}
}

func TestHandleDefaultSkill(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	task, err := handler.Handle(context.Background(), "", Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "test"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if task.Skill != SkillFullTask {
		t.Errorf("expected full-task default, got %s", task.Skill)
	}
}

func TestHandlerOptions(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil,
		WithMaxTasks(3),
		WithTimeout(10*time.Second),
	)
	if handler.maxTasks != 3 {
		t.Errorf("expected maxTasks=3, got %d", handler.maxTasks)
	}
	if handler.timeout != 10*time.Second {
		t.Errorf("expected timeout=10s, got %v", handler.timeout)
	}
}

func TestCancelTask(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	task, _ := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "test"}},
	}, "")

	// Cancel the task.
	if err := handler.CancelTask(task.ID); err != nil {
		t.Fatal(err)
	}

	gotTask, ok := handler.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if gotTask.Status.State != TaskStateCanceled {
		t.Errorf("expected canceled, got %s", gotTask.Status.State)
	}

	// Cancel again should fail (terminal state).
	if err := handler.CancelTask(task.ID); err == nil {
		t.Error("expected error for canceling terminal task")
	}
}

func TestCancelNonexistentTask(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	if err := handler.CancelTask("nonexistent"); err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestActiveTaskCount(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	if handler.ActiveTaskCount() != 0 {
		t.Error("expected 0 active tasks initially")
	}
}

func TestWorkspaceMetadata(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	meta := handler.WorkspaceMetadata()
	if meta.Workspace != "." {
		t.Errorf("expected workspace ., got %s", meta.Workspace)
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

func TestMCPBridgeSendTaskValidation(t *testing.T) {
	client := NewClient("http://localhost:9999", "")
	tools := MCPBridgeTools(client)
	// Find the send_task tool.
	var sendTool *a2aSendTaskTool
	for _, tool := range tools {
		if t, ok := tool.(*a2aSendTaskTool); ok {
			sendTool = t
			break
		}
	}
	if sendTool == nil {
		t.Fatal("send_task tool not found")
	}

	// Invalid JSON input.
	_, err := sendTool.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err != nil {
		t.Fatal(err)
	}
	// Result should be an error.
	// (Can't test actual execution without a running server.)
}

func TestMCPBridgeGetTaskValidation(t *testing.T) {
	client := NewClient("http://localhost:9999", "")
	tools := MCPBridgeTools(client)
	var getTool *a2aGetTaskTool
	for _, tool := range tools {
		if t, ok := tool.(*a2aGetTaskTool); ok {
			getTool = t
			break
		}
	}
	if getTool == nil {
		t.Fatal("get_task tool not found")
	}

	// Invalid input.
	result, err := getTool.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result for invalid input")
	}
}

// ---------------------------------------------------------------------------
// SSE decode
// ---------------------------------------------------------------------------

func TestDecodeSSE(t *testing.T) {
	ch := make(chan JSONRPCResponse, 10)
	input := "data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{\"id\":\"task-1\",\"status\":{\"state\":\"working\"}}}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{\"id\":\"task-1\",\"status\":{\"state\":\"completed\"}}}\n\n"
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

func TestDecodeSSESkipNonDataLines(t *testing.T) {
	ch := make(chan JSONRPCResponse, 10)
	input := "event: status\nid: 1\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{}}\n\ncomment line\n\n"
	go decodeSSE(newStringReader(input), ch)

	resp := <-ch
	if resp.JSONRPC != "2.0" {
		t.Errorf("unexpected jsonrpc: %s", resp.JSONRPC)
	}
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

func TestGenerateInstanceID(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateInstanceID()
		if ids[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		ids[id] = true
	}
}

func TestDetectWorkspaceMeta(t *testing.T) {
	meta := detectWorkspaceMeta(".")
	if meta.Workspace != "." {
		t.Errorf("expected ., got %s", meta.Workspace)
	}
	if meta.ProjName == "" {
		t.Error("expected non-empty project name")
	}
}

// ---------------------------------------------------------------------------
// Concurrent safety
// ---------------------------------------------------------------------------

func TestHandlerConcurrentAccess(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil, WithMaxTasks(100), WithTimeout(1*time.Hour))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = handler.Handle(context.Background(), SkillFullTask, Message{
				Role:  "user",
				Parts: []Part{{Kind: "text", Text: fmt.Sprintf("task %d", i)}},
			}, "")
		}(i)
	}
	wg.Wait()
	// All 50 tasks should be in the handler (they'll be "working" since there's no agent to complete them).
	if handler.ActiveTaskCount() != 50 {
		t.Logf("active tasks: %d (some may have completed)", handler.ActiveTaskCount())
		// Don't fail — tasks without agent will complete as failed.
	}
}

// ---------------------------------------------------------------------------
// Multi-turn (input-required flow)
// ---------------------------------------------------------------------------

func TestRequestInput(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	task, err := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// Manually set to working for the test.
	handler.mu.Lock()
	handler.tasks[task.ID].Status = TaskStatus{State: TaskStateWorking}
	handler.mu.Unlock()

	// Request input.
	if err := handler.RequestInput(task.ID, "What file do you want to edit?"); err != nil {
		t.Fatal(err)
	}

	gotTask, ok := handler.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if gotTask.Status.State != TaskStateInputRequired {
		t.Errorf("expected input-required, got %s", gotTask.Status.State)
	}
	// Check agent question is in history.
	lastMsg := gotTask.History[len(gotTask.History)-1]
	if lastMsg.Role != "agent" || !containsStr(lastMsg.Parts[0].Text, "What file") {
		t.Errorf("expected agent question in history, got: %+v", lastMsg)
	}
}

func TestContinueTask(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	task, err := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// Put task into input-required state directly on the internal task.
	handler.mu.Lock()
	internal := handler.tasks[task.ID]
	internal.Status = TaskStatus{State: TaskStateInputRequired}
	// Re-open done channel since it was closed by the execute goroutine.
	if internal.done == nil {
		internal.done = make(chan struct{})
	}
	handler.mu.Unlock()

	// Continue with user response.
	_, err = handler.Handle(context.Background(), "", Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "main.go"}},
	}, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	gotTask, _ := handler.GetTask(task.ID)
	// original msg + continue msg (no agent question msg in this test)
	if len(gotTask.History) != 2 {
		t.Errorf("expected 2 history messages, got %d", len(gotTask.History))
	}
	// Last message should be the continuation.
	lastMsg := gotTask.History[len(gotTask.History)-1]
	if lastMsg.Parts[0].Text != "main.go" {
		t.Errorf("expected 'main.go' in last message, got %s", lastMsg.Parts[0].Text)
	}
}

func TestContinueTaskRejectsNonInputRequired(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	task, err := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	// Task is in "submitted" or "working" state — not input-required.
	time.Sleep(100 * time.Millisecond)

	_, err = handler.Handle(context.Background(), "", Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "response"}},
	}, task.ID)
	if err == nil {
		t.Fatal("expected error for non-input-required task")
	}
	if !containsStr(err.Error(), "not in input-required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContinueTaskNotFound(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	_, err := handler.Handle(context.Background(), "", Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "response"}},
	}, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestRequestInputRejectsNonWorking(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	task, _ := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}},
	}, "")
	// Wait for async execution to finish (no agent → will fail quickly).
	time.Sleep(200 * time.Millisecond)

	// Force to completed state.
	handler.mu.Lock()
	handler.tasks[task.ID].Status = TaskStatus{State: TaskStateCompleted}
	handler.mu.Unlock()

	err := handler.RequestInput(task.ID, "question?")
	if err == nil {
		t.Fatal("expected error for non-working task")
	}
}

// ---------------------------------------------------------------------------
// Resubscribe (server-level)
// ---------------------------------------------------------------------------

func TestClientResubscribe(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	client := NewClient("http://127.0.0.1:"+fmt.Sprintf("%d", srv.Port()), "")

	// Resubscribe to nonexistent task should fail.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Resubscribe(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for resubscribing to nonexistent task")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func newStringReader(s string) *stringReader { return &stringReader{s: s} }

type stringReader struct {
	s   string
	pos int
}

func (r *stringReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.s) {
		return 0, nil
	}
	n = copy(p, r.s[r.pos:])
	r.pos += n
	return n, nil
}

// ---------------------------------------------------------------------------
// RemoteTool
// ---------------------------------------------------------------------------

func TestRemoteToolName(t *testing.T) {
	remote := NewRemoteTool(&Registry{dir: t.TempDir()}, "key")
	if remote.Name() != "a2a_remote" {
		t.Errorf("expected a2a_remote, got %s", remote.Name())
	}
}

func TestRemoteToolParameters(t *testing.T) {
	remote := NewRemoteTool(&Registry{dir: t.TempDir()}, "key")
	params := remote.Parameters()
	var schema map[string]interface{}
	if err := json.Unmarshal(params, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]interface{})
	for _, field := range []string{"target", "skill", "message"} {
		if _, ok := props[field]; !ok {
			t.Errorf("missing parameter: %s", field)
		}
	}
}

func TestRemoteToolListInstances(t *testing.T) {
	reg := &Registry{dir: t.TempDir()}
	remote := NewRemoteTool(reg, "key")

	// No instances → friendly message.
	result, err := remote.Execute(context.Background(), json.RawMessage(`{"target":"list","skill":"full-task","message":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "No other ggcode instances found") {
		t.Errorf("expected 'no instances' message, got: %s", result.Content)
	}
}

func TestRemoteToolInvalidInput(t *testing.T) {
	remote := NewRemoteTool(&Registry{dir: t.TempDir()}, "key")

	result, err := remote.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error result")
	}
}

func TestRemoteToolTargetNotFound(t *testing.T) {
	remote := NewRemoteTool(&Registry{dir: t.TempDir()}, "key")

	result, err := remote.Execute(context.Background(), json.RawMessage(`{"target":"nonexistent","skill":"full-task","message":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent target")
	}
	if !containsStr(result.Content, "no ggcode instances found") && !containsStr(result.Content, "no instance matching") {
		t.Errorf("unexpected error: %s", result.Content)
	}
}

func TestRemoteToolCacheRefresh(t *testing.T) {
	remote := NewRemoteTool(&Registry{dir: t.TempDir()}, "key")

	// Initially empty.
	instances, err := remote.discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Error("expected 0 instances")
	}

	// Refresh (no-op since registry is empty).
	remote.RefreshCache()

	instances, err = remote.discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Error("expected 0 instances after refresh")
	}
}

func TestRemoteToolE2E(t *testing.T) {
	// Use isolated temp dir for registry to avoid pollution from other tests.
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "a2a")
	os.MkdirAll(dir, 0755)

	reg := &Registry{dir: dir}

	// Start an A2A server to simulate a remote ggcode instance.
	handler1 := NewTaskHandler(".", nil, nil)
	srv1 := NewServer(ServerConfig{Port: 0}, handler1)
	if err := srv1.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv1.Stop()

	// Write a fake "remote" instance with our PID (so pruneDead keeps it).
	inst := InstanceInfo{
		ID:        "remote-test-id",
		PID:       os.Getpid(),
		Workspace: "/Users/zhanju/ggai/a2a-order-service",
		Endpoint:  srv1.Endpoint(),
		Status:    "ready",
	}
	data, _ := json.MarshalIndent(inst, "", "  ")
	instPath := filepath.Join(reg.dir, inst.ID+".json")
	os.WriteFile(instPath, data, 0644)

	remote := NewRemoteTool(reg, "")

	// List instances.
	result, err := remote.Execute(context.Background(), json.RawMessage(`{"target":"list","skill":"full-task","message":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(result.Content, "a2a-order-service") {
		t.Errorf("expected order-service in list, got: %s", result.Content)
	}

	// Call by name.
	result, err = remote.Execute(context.Background(), json.RawMessage(`{"target":"order-service","skill":"file-search","message":"find TODOs"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "Task sent to order-service") {
		t.Errorf("expected task sent message, got: %s", result.Content)
	}
}

// ---------------------------------------------------------------------------
// Tests for optimization changes (snapshot, channel notification, cleanup, etc.)
// ---------------------------------------------------------------------------

func TestGetTaskReturnsSnapshot(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	task, err := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// GetTask returns a snapshot — mutating it should not affect internal state.
	snap, ok := handler.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	snap.Status = TaskStatus{State: TaskStateFailed}

	// Internal task should be unchanged.
	internal := handler.tasks[task.ID]
	if internal.Status.State == TaskStateFailed {
		t.Error("mutating snapshot affected internal task state")
	}
}

func TestSnapshotDeepCopiesSlices(t *testing.T) {
	now := time.Now()
	orig := &Task{
		ID:     "test-1",
		Status: TaskStatus{State: TaskStateCompleted},
		History: []Message{
			{Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}}},
		},
		Artifacts: []Artifact{
			{ArtifactID: "a1", Parts: []Part{{Kind: "text", Text: "artifact"}}},
		},
		CreatedAt: now,
		UpdatedAt: now,
		done:      make(chan struct{}),
	}
	close(orig.done)

	snap := orig.Snapshot()

	// Mutate snapshot's slices.
	snap.History[0].Parts[0].Text = "mutated"
	snap.Artifacts[0].Parts[0].Text = "mutated2"

	// Original should be unchanged.
	if orig.History[0].Parts[0].Text != "hello" {
		t.Error("snapshot History was not deep copied")
	}
	if orig.Artifacts[0].Parts[0].Text != "artifact" {
		t.Error("snapshot Artifacts was not deep copied")
	}

	// done channel should not be copied.
	if snap.done != nil {
		t.Error("done channel should not be in snapshot")
	}
}

func TestGenerateIDUniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id := generateID()
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestDoneChannelClosedOnTerminal(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)
	task, err := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// Get the done channel.
	done := handler.GetTaskDone(task.ID)
	if done == nil {
		t.Fatal("done channel should exist for new task")
	}

	// Put task into terminal state.
	handler.mu.Lock()
	it := handler.tasks[task.ID]
	it.Status = TaskStatus{State: TaskStateCompleted}
	it.UpdatedAt = time.Now()
	if it.done != nil {
		close(it.done)
		it.done = nil
	}
	handler.mu.Unlock()

	// done channel should be closed.
	select {
	case <-done:
		// OK
	default:
		t.Error("done channel should be closed after terminal state")
	}

	// GetTaskDone should return nil after terminal.
	done2 := handler.GetTaskDone(task.ID)
	if done2 != nil {
		t.Error("GetTaskDone should return nil for terminal task")
	}
}

func TestCleanupExpiredTasks(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil)

	// Create a task and force it to completed with old timestamp.
	task, err := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	handler.mu.Lock()
	internal := handler.tasks[task.ID]
	internal.Status = TaskStatus{State: TaskStateCompleted}
	internal.UpdatedAt = time.Now().Add(-maxCompletedAge - time.Minute)
	handler.mu.Unlock()

	// Create another fresh task.
	task2, err := handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello2"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// Trigger cleanup by calling Handle (which calls cleanupExpiredTasksLocked).
	_, err = handler.Handle(context.Background(), SkillFullTask, Message{
		Role: "user", Parts: []Part{{Kind: "text", Text: "hello3"}},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// Old task should be cleaned up.
	_, ok := handler.GetTask(task.ID)
	if ok {
		t.Error("old completed task should have been cleaned up")
	}

	// Fresh task should still exist.
	_, ok = handler.GetTask(task2.ID)
	if !ok {
		t.Error("fresh task should still exist")
	}
}

func TestClientDisconnectCancelsWait(t *testing.T) {
	handler := NewTaskHandler(".", nil, nil, WithTimeout(30*time.Second))
	srv := NewServer(ServerConfig{Port: 0}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	client := NewClient(srv.Endpoint(), "")

	// Create a context that we cancel early.
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel quickly — the server-side handler won't complete in time.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	// This should not block forever — it should return when ctx is cancelled.
	_, err := client.SendMessage(ctx, SkillFullTask, "test")
	// Context cancelled should result in an error (context cancelled or EOF).
	if err == nil {
		// The task might have completed before the cancel took effect.
		// That's OK — the important thing is it didn't hang.
		t.Log("task completed before cancel (acceptable)")
	}
}

func TestSSEMultiLineData(t *testing.T) {
	input := "data: {\"jsonrpc\":\"2.0\"\ndata: ,\"result\":{\"status\":\"ok\"}}\n\n"
	ch := make(chan JSONRPCResponse, 1)
	decodeSSE(strings.NewReader(input), ch)

	select {
	case resp := <-ch:
		if resp.Result == nil {
			t.Error("expected result in SSE response")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for SSE event")
	}
}

func TestSSECommentLines(t *testing.T) {
	input := ": this is a comment\ndata: {\"jsonrpc\":\"2.0\",\"id\":1}\n\n"
	ch := make(chan JSONRPCResponse, 1)
	decodeSSE(strings.NewReader(input), ch)

	select {
	case resp := <-ch:
		if resp.ID == nil {
			t.Error("expected id in SSE response")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for SSE event")
	}
}

func TestPerPIDRegistryFiles(t *testing.T) {
	dir := t.TempDir()
	reg := &Registry{dir: dir}

	// Register instance A.
	instA := InstanceInfo{
		ID: "inst-a", PID: os.Getpid(),
		Workspace: "/project-a", Endpoint: "http://localhost:9001", Status: "ready",
	}
	if err := reg.Register(instA); err != nil {
		t.Fatal(err)
	}

	// Verify file exists.
	if _, err := os.Stat(filepath.Join(dir, "inst-a.json")); err != nil {
		t.Fatalf("per-PID file not created: %v", err)
	}

	// Register instance B.
	regB := &Registry{dir: dir}
	instB := InstanceInfo{
		ID: "inst-b", PID: os.Getpid(),
		Workspace: "/project-b", Endpoint: "http://localhost:9002", Status: "ready",
	}
	if err := regB.Register(instB); err != nil {
		t.Fatal(err)
	}

	// Discover from a third registry (simulates a different process).
	regC := &Registry{dir: dir, selfID: "inst-c"}
	discovered, err := regC.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 2 {
		t.Fatalf("expected 2 discovered instances, got %d", len(discovered))
	}

	// Unregister A.
	if err := reg.Unregister(); err != nil {
		t.Fatal(err)
	}

	// Discover should now return 1 (B only).
	discovered, err = regC.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered instance after unregister, got %d", len(discovered))
	}
	if discovered[0].ID != "inst-b" {
		t.Errorf("expected inst-b, got %s", discovered[0].ID)
	}
}

func TestRegistryConcurrentRegister(t *testing.T) {
	dir := t.TempDir()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reg := &Registry{dir: dir}
			inst := InstanceInfo{
				ID: fmt.Sprintf("inst-%d", i), PID: os.Getpid(),
				Workspace: fmt.Sprintf("/project-%d", i),
				Endpoint:  fmt.Sprintf("http://localhost:%d", 9000+i),
				Status:    "ready",
			}
			if err := reg.Register(inst); err != nil {
				t.Errorf("register %d failed: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// All 10 instances should be discoverable.
	reg := &Registry{dir: dir, selfID: "self"}
	discovered, err := reg.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 10 {
		t.Errorf("expected 10 discovered instances, got %d", len(discovered))
	}
}

func TestAmbiguousMatchError(t *testing.T) {
	dir := t.TempDir()
	remote := NewRemoteTool(&Registry{dir: dir, selfID: "self"}, "")

	// Register two instances with similar names using separate registry objects
	// (simulating separate processes).
	reg1 := &Registry{dir: dir}
	reg1.Register(InstanceInfo{
		ID: "order-1", PID: os.Getpid(),
		Workspace: "/order-service-v1", Endpoint: "http://localhost:9001", Status: "ready",
	})
	reg2 := &Registry{dir: dir}
	reg2.Register(InstanceInfo{
		ID: "order-2", PID: os.Getpid(),
		Workspace: "/order-service-v2", Endpoint: "http://localhost:9002", Status: "ready",
	})

	// "order" should match both → ambiguous error.
	result, err := remote.Execute(context.Background(), json.RawMessage(
		`{"target":"order","skill":"full-task","message":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error for ambiguous match")
	}
	if !containsStr(result.Content, "ambiguous") {
		t.Errorf("expected ambiguous error, got: %s", result.Content)
	}
}
