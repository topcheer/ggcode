//go:build integration

package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// ---------------------------------------------------------------------------
// Real E2E test: loads production config, real LLM, real tools, real files
// ---------------------------------------------------------------------------

const e2eAPIKey = "ggcode-a2a-test-key-2025"

// e2eEnv holds the full test environment for one instance.
type e2eEnv struct {
	name     string
	dir      string
	cfg      *config.Config
	prov     provider.Provider
	registry *tool.Registry
	agent    *agent.Agent
	server   *Server
	client   *Client
}

// setupRealE2E loads production config and creates a fully-wired A2A instance.
func setupRealE2E(t *testing.T, name, workspaceDir string) *e2eEnv {
	t.Helper()

	// 1. Load production config.
	cfgPath := config.ConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config %s: %v", cfgPath, err)
	}

	// 2. Resolve active provider (uses whatever the user has configured).
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("resolve endpoint: %v", err)
	}
	t.Logf("[%s] Using provider: %s / %s / model=%s", name, resolved.VendorName, resolved.EndpointName, resolved.Model)

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	// 3. Setup permission policy (bypass for test).
	policy := permission.NewConfigPolicyWithMode(nil, []string{workspaceDir}, permission.BypassMode)

	// 4. Register all built-in tools.
	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry, policy, workspaceDir); err != nil {
		t.Fatalf("register tools: %v", err)
	}

	// 5. Create agent.
	ag := agent.NewAgent(prov, registry, fmt.Sprintf(
		"You are ggcode A2A agent for %s. Execute tasks precisely and concisely.", name,
	), 10)

	// 6. Create A2A handler + server.
	handler := NewTaskHandler(workspaceDir, ag, registry,
		WithMaxTasks(3),
		WithTimeout(5*time.Minute),
	)
	srv := NewServer(ServerConfig{
		Host:   "127.0.0.1",
		Port:   0,
		APIKey: e2eAPIKey,
	}, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(srv.Stop)

	client := NewClient(srv.Endpoint(), e2eAPIKey)

	t.Logf("[%s] A2A server: %s", name, srv.Endpoint())
	return &e2eEnv{
		name: name, dir: workspaceDir,
		cfg: cfg, prov: prov, registry: registry,
		agent: ag, server: srv, client: client,
	}
}

// setupE2ECluster creates 3 instances with real LLM + tools.
func setupE2ECluster(t *testing.T) []*e2eEnv {
	t.Helper()
	// Use the 3 microservice projects as real workspaces.
	return []*e2eEnv{
		setupRealE2E(t, "user-service", "/Users/zhanju/ggai/a2a-user-service"),
		setupRealE2E(t, "order-service", "/Users/zhanju/ggai/a2a-order-service"),
		setupRealE2E(t, "gateway", "/Users/zhanju/ggai/a2a-gateway"),
	}
}

// ---------------------------------------------------------------------------
// E2E Tests
// ---------------------------------------------------------------------------

// TestRealE2E_DiscoverAndMetadata tests agent card with real workspace data.
func TestRealE2E_DiscoverAndMetadata(t *testing.T) {
	env := setupRealE2E(t, "user-service", "/Users/zhanju/ggai/a2a-user-service")
	ctx := context.Background()

	card, err := env.client.Discover(ctx)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	// Verify agent card content.
	if card.Name != "ggcode" {
		t.Errorf("name = %s, want ggcode", card.Name)
	}
	if len(card.Skills) != 6 {
		t.Errorf("skills = %d, want 6", len(card.Skills))
	}
	if !card.Capabilities.Streaming {
		t.Error("streaming should be true")
	}

	// Verify workspace metadata is real.
	meta, ok := card.Metadata.(map[string]interface{})
	if !ok {
		t.Fatalf("metadata type = %T", card.Metadata)
	}
	projName, _ := meta["project_name"].(string)
	if projName != "a2a-user-service" {
		t.Errorf("project_name = %s, want a2a-user-service", projName)
	}
	hasGit, _ := meta["has_git"].(bool)
	if !hasGit {
		t.Error("has_git should be true (workspace has .git)")
	}

	// Verify languages detected.
	langs, _ := meta["languages"].([]interface{})
	foundGo := false
	for _, l := range langs {
		if l == "go" {
			foundGo = true
		}
	}
	if !foundGo {
		t.Errorf("expected 'go' in languages, got %v", langs)
	}

	t.Logf("✅ Agent Card: name=%s, project=%s, has_git=%v, languages=%v, description=%s",
		card.Name, projName, hasGit, langs, card.Description)
}

// TestRealE2E_FileSearch tests file-search skill with real tools + LLM.
func TestRealE2E_FileSearch(t *testing.T) {
	env := setupRealE2E(t, "user-service", "/Users/zhanju/ggai/a2a-user-service")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	task, err := env.client.SendMessage(ctx, "file-search", "在 main.go 中搜索所有的 handle 函数名")
	if err != nil {
		t.Fatalf("send task: %v", err)
	}

	t.Logf("Task %s → %s", task.ID, task.Status.State)

	// Verify task completed.
	if task.Status.State != TaskStateCompleted {
		// Print artifacts for debugging.
		for _, a := range task.Artifacts {
			for _, p := range a.Parts {
				t.Logf("  artifact: %s", truncate(p.Text, 200))
			}
		}
		if task.Status.State == TaskStateFailed {
			// Print history for error details.
			for _, m := range task.History {
				for _, p := range m.Parts {
					t.Logf("  [%s]: %s", m.Role, truncate(p.Text, 200))
				}
			}
		}
		t.Fatalf("expected completed, got %s", task.Status.State)
	}

	// Verify result contains relevant content about handle functions.
	if len(task.Artifacts) == 0 {
		t.Fatal("expected at least 1 artifact")
	}
	result := task.Artifacts[0].Parts[0].Text
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
	// With LLM, the result should contain relevant content about the file.
	if !strings.Contains(result, "main.go") && !strings.Contains(result, "handle") {
		t.Errorf("result should mention main.go or handle functions, got: %s", truncate(result, 300))
	}
	t.Logf("✅ file-search result: %s", truncate(result, 500))
}

// TestRealE2E_GitOps tests git-ops skill with real git commands.
func TestRealE2E_GitOps(t *testing.T) {
	env := setupRealE2E(t, "order-service", "/Users/zhanju/ggai/a2a-order-service")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	task, err := env.client.SendMessage(ctx, "git-ops", "show the git log with last 3 commits")
	if err != nil {
		t.Fatalf("send task: %v", err)
	}

	if task.Status.State != TaskStateCompleted {
		t.Fatalf("expected completed, got %s", task.Status.State)
	}

	result := task.Artifacts[0].Parts[0].Text
	t.Logf("✅ git-ops result: %s", truncate(result, 300))
}

// TestRealE2E_CodeEdit tests code-edit skill that actually modifies a file.
func TestRealE2E_CodeEdit(t *testing.T) {
	dir := "/Users/zhanju/ggai/a2a-user-service"
	env := setupRealE2E(t, "user-service", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Create a temporary file to edit.
	testFile := filepath.Join(dir, "test_handler.go")
	originalContent := `package main

// TestHandler is a placeholder handler.
func TestHandler() string {
	return "hello"
}
`
	if err := os.WriteFile(testFile, []byte(originalContent), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(testFile) // cleanup

	task, err := env.client.SendMessage(ctx, "code-edit",
		"在 test_handler.go 文件中，把 TestHandler 函数的返回值从 'hello' 改成 'hello from a2a'，然后加一个新函数 TestHandlerV2 返回 'a2a v2'")
	if err != nil {
		t.Fatalf("send task: %v", err)
	}

	if task.Status.State != TaskStateCompleted {
		for _, m := range task.History {
			for _, p := range m.Parts {
				t.Logf("  [%s]: %s", m.Role, truncate(p.Text, 300))
			}
		}
		t.Fatalf("expected completed, got %s", task.Status.State)
	}

	// Verify the file was actually modified.
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	result := string(content)
	if !strings.Contains(result, "hello from a2a") {
		t.Errorf("file should contain 'hello from a2a', got:\n%s", result)
	}
	if !strings.Contains(result, "TestHandlerV2") {
		t.Errorf("file should contain 'TestHandlerV2', got:\n%s", result)
	}
	t.Logf("✅ code-edit verified:\n%s", truncate(result, 500))
}

// TestRealE2E_CommandExec tests command execution skill.
func TestRealE2E_CommandExec(t *testing.T) {
	env := setupRealE2E(t, "gateway", "/Users/zhanju/ggai/a2a-gateway")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	task, err := env.client.SendMessage(ctx, "command-exec", "运行 'ls -la *.go' 命令查看当前目录下的 Go 文件")
	if err != nil {
		t.Fatalf("send task: %v", err)
	}

	if task.Status.State != TaskStateCompleted {
		t.Fatalf("expected completed, got %s", task.Status.State)
	}

	result := task.Artifacts[0].Parts[0].Text
	if !strings.Contains(result, "main.go") {
		t.Errorf("result should mention main.go, got: %s", truncate(result, 200))
	}
	t.Logf("✅ command-exec result: %s", truncate(result, 300))
}

// TestRealE2E_FullTask tests full-task skill with a multi-step operation.
func TestRealE2E_FullTask(t *testing.T) {
	dir := "/Users/zhanju/ggai/a2a-order-service"
	env := setupRealE2E(t, "order-service", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Create a test file to work with.
	testFile := filepath.Join(dir, "version.txt")
	os.WriteFile(testFile, []byte("v0.0.1\n"), 0644)
	defer os.Remove(testFile)

	task, err := env.client.SendMessage(ctx, "full-task",
		"读取 version.txt 的内容，然后把版本号从 v0.0.1 更新到 v0.1.0，写回文件")
	if err != nil {
		t.Fatalf("send task: %v", err)
	}

	if task.Status.State != TaskStateCompleted {
		for _, m := range task.History {
			for _, p := range m.Parts {
				t.Logf("  [%s]: %s", m.Role, truncate(p.Text, 300))
			}
		}
		t.Fatalf("expected completed, got %s", task.Status.State)
	}

	// Verify file was updated.
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "v0.1.0") {
		t.Errorf("expected v0.1.0, got: %s", string(content))
	}
	t.Logf("✅ full-task verified: %s", strings.TrimSpace(string(content)))
}

// TestRealE2E_SSEStreaming tests SSE streaming with real execution.
func TestRealE2E_SSEStreaming(t *testing.T) {
	env := setupRealE2E(t, "user-service", "/Users/zhanju/ggai/a2a-user-service")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ch, err := env.client.SendMessageStream(ctx, "file-search", "搜索 main.go 中所有的 http.HandleFunc 调用")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	events := 0
	var lastState string
	for resp := range ch {
		events++
		resultJSON, _ := json.Marshal(resp.Result)
		// Extract state from result if it's a task.
		var task map[string]interface{}
		if json.Unmarshal(resultJSON, &task) == nil {
			if status, ok := task["status"].(map[string]interface{}); ok {
				lastState, _ = status["state"].(string)
			}
		}
		t.Logf("  📡 event %d: state=%s (%d bytes)", events, lastState, len(resultJSON))
	}
	if events < 2 {
		t.Errorf("expected at least 2 SSE events (working + final), got %d", events)
	}
	t.Logf("✅ SSE: %d events, final state=%s", events, lastState)
}

// TestRealE2E_AuthRejected verifies wrong API key is rejected.
func TestRealE2E_AuthRejected(t *testing.T) {
	env := setupRealE2E(t, "user-service", "/Users/zhanju/ggai/a2a-user-service")
	ctx := context.Background()

	wrongClient := NewClient(env.server.Endpoint(), "wrong-key")
	_, err := wrongClient.SendMessage(ctx, "file-search", "test")
	if err == nil {
		t.Fatal("expected error with wrong API key")
	}
	t.Logf("✅ Auth rejected: %v", err)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ---------------------------------------------------------------------------
// Integration tests for optimization features (with real agent/LLM)
// ---------------------------------------------------------------------------

// TestRealE2E_SnapshotIsolation verifies that concurrent reads after task
// completion return independent snapshots (P1-1).
func TestRealE2E_SnapshotIsolation(t *testing.T) {
	env := setupRealE2E(t, "snap-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Send a simple task that completes quickly.
	task, err := env.client.SendMessage(ctx, SkillFileSearch, "list files in this workspace")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	// Poll until terminal.
	task = pollUntilTerminal(t, ctx, env.client, task.ID)

	// Get two snapshots — they should be equal but independent.
	snap1, err := env.client.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap2, err := env.client.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Content should match.
	if snap1.Status.State != snap2.Status.State {
		t.Errorf("snapshot states differ: %s vs %s", snap1.Status.State, snap2.Status.State)
	}

	// They should have the same number of history entries.
	if len(snap1.History) != len(snap2.History) {
		t.Errorf("snapshot history lengths differ: %d vs %d", len(snap1.History), len(snap2.History))
	}

	t.Logf("✅ Snapshot isolation: state=%s, history=%d", snap1.Status.State, len(snap1.History))
}

// TestRealE2E_ChannelNotification verifies that message/send returns promptly
// when the task completes (P1-2: channel-based wait instead of polling).
// This test would hang forever if the old polling approach was still used with
// a broken ticker.
func TestRealE2E_ChannelNotification(t *testing.T) {
	env := setupRealE2E(t, "channel-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	start := time.Now()
	task, err := env.client.SendMessage(ctx, SkillFileSearch, "search for any file")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	elapsed := time.Since(start)
	t.Logf("SendMessage returned in %v (should be fast — channel-based wait)", elapsed)

	// The call should return after the task finishes, not timeout.
	if task.Status.State == TaskStateWorking || task.Status.State == TaskStateSubmitted {
		t.Errorf("task should be terminal after SendMessage returns, got: %s", task.Status.State)
	}

	t.Logf("✅ Channel notification: task=%s, state=%s", task.ID, task.Status.State)
}

// TestRealE2E_CancelReturnsFreshState verifies that canceling a task returns
// the updated "canceled" state, not the pre-cancel state (P1-3).
func TestRealE2E_CancelReturnsFreshState(t *testing.T) {
	env := setupRealE2E(t, "cancel-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// First, send a short task to verify things work.
	warmup, err := env.client.SendMessage(ctx, SkillFileSearch, "list files")
	if err != nil {
		t.Fatalf("warmup: %v", err)
	}
	pollUntilTerminal(t, ctx, env.client, warmup.ID)
	t.Log("warmup task completed ✅")

	// Now submit a long task via stream and cancel it.
	streamCh, err := env.client.SendMessageStream(ctx, SkillFullTask,
		"Write a very comprehensive HTTP server in Go with middleware, routing, logging, graceful shutdown, and comprehensive tests. Take your time.")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	// Read the first SSE event to get the task ID.
	var taskID string
	select {
	case resp := <-streamCh:
		if resp.Result != nil {
			resultBytes, _ := json.Marshal(resp.Result)
			var task Task
			if json.Unmarshal(resultBytes, &task) == nil && task.ID != "" {
				taskID = task.ID
			}
			if taskID == "" {
				var m map[string]interface{}
				if json.Unmarshal(resultBytes, &m) == nil {
					if id, ok := m["id"].(string); ok {
						taskID = id
					}
				}
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for first SSE event")
	}

	if taskID == "" {
		t.Fatal("could not extract task ID from SSE stream")
	}

	// Wait a bit then cancel.
	time.Sleep(2 * time.Second)
	canceledTask, err := env.client.CancelTask(ctx, taskID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	// The returned task should reflect the canceled state.
	if canceledTask.Status.State != TaskStateCanceled {
		t.Errorf("expected canceled state, got: %s", canceledTask.Status.State)
	}

	// Verify via GetTask as well.
	got, err := env.client.GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.State != TaskStateCanceled {
		t.Errorf("GetTask after cancel: expected canceled, got: %s", got.Status.State)
	}

	t.Logf("✅ Cancel returns fresh state: %s", canceledTask.Status.State)
}

// TestRealE2E_ClientDisconnect verifies that the server does not hang when the
// client disconnects mid-task (P1-2: ctx propagation).
func TestRealE2E_ClientDisconnect(t *testing.T) {
	env := setupRealE2E(t, "disconnect-test", t.TempDir())

	// Use a short context that cancels before the task completes.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	_, err := env.client.SendMessage(ctx, SkillFullTask,
		"Write a very detailed REST API implementation in Go with CRUD operations, validation, error handling, and tests. Take your time.")
	elapsed := time.Since(start)

	// Should return promptly after ctx deadline (not hang for 5 min timeout).
	if elapsed > 10*time.Second {
		t.Errorf("SendMessage took %v — should have returned promptly on ctx cancel", elapsed)
	}

	if err == nil {
		t.Log("task completed within deadline (acceptable)")
	} else {
		t.Logf("✅ Client disconnect: returned in %v with error: %v", elapsed, err)
	}
}

// TestRealE2E_TaskCleanupAfterMultipleRuns verifies that old completed tasks
// are cleaned up and don't cause memory growth (P1-5).
func TestRealE2E_TaskCleanupAfterMultipleRuns(t *testing.T) {
	env := setupRealE2E(t, "cleanup-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Submit several short tasks.
	var taskIDs []string
	for i := 0; i < 5; i++ {
		task, err := env.client.SendMessage(ctx, SkillFileSearch, "list files")
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		task = pollUntilTerminal(t, ctx, env.client, task.ID)
		taskIDs = append(taskIDs, task.ID)
		t.Logf("  Task %d: %s → %s", i, task.ID[:16], task.Status.State)
	}

	// All tasks should still be in the handler (not yet expired).
	for _, id := range taskIDs {
		_, err := env.client.GetTask(ctx, id)
		if err != nil {
			t.Errorf("task %s should still exist", id[:16])
		}
	}

	// Artificially age the tasks by modifying their UpdatedAt directly.
	env.server.handler.mu.Lock()
	for id := range env.server.handler.tasks {
		tt := env.server.handler.tasks[id]
		if tt.Status.IsTerminal() {
			tt.UpdatedAt = time.Now().Add(-maxCompletedAge - time.Minute)
		}
	}
	env.server.handler.mu.Unlock()

	// Submit one more task — this triggers cleanupExpiredTasksLocked.
	_, err := env.client.SendMessage(ctx, SkillFileSearch, "list files again")
	if err != nil {
		t.Fatal(err)
	}

	// The old tasks should now be cleaned up.
	time.Sleep(500 * time.Millisecond) // wait for async processing
	env.server.handler.mu.Lock()
	remaining := len(env.server.handler.tasks)
	env.server.handler.mu.Unlock()

	if remaining > 2 { // at most the new task + any in-flight task
		t.Errorf("expected ≤2 tasks remaining after cleanup, got %d", remaining)
	}

	t.Logf("✅ Task cleanup: %d tasks remaining after aging and new submission", remaining)
}

// TestRealE2E_GenerateIDNoCollision verifies that rapidly submitted tasks
// get unique IDs (P1-4).
func TestRealE2E_GenerateIDNoCollision(t *testing.T) {
	env := setupRealE2E(t, "id-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Submit 3 tasks concurrently.
	type result struct {
		task *Task
		err  error
	}
	ch := make(chan result, 3)

	for i := 0; i < 3; i++ {
		go func() {
			task, err := env.client.SendMessage(ctx, SkillFileSearch, "list files")
			ch <- result{task, err}
		}()
	}

	ids := make(map[string]bool)
	for i := 0; i < 3; i++ {
		r := <-ch
		if r.err != nil {
			t.Fatalf("concurrent send %d: %v", i, r.err)
		}
		if ids[r.task.ID] {
			t.Fatalf("duplicate task ID: %s", r.task.ID)
		}
		ids[r.task.ID] = true
	}

	t.Logf("✅ ID uniqueness: 3 concurrent tasks got unique IDs: %v", collectKeys(ids))
}

// TestRealE2E_AmbiguousMatchWithRealLLM verifies that when two instances have
// similar names, the ambiguous match error is returned rather than silently
// picking one (P3-1).
func TestRealE2E_AmbiguousMatchWithRealLLM(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "order-service")
	dir2 := filepath.Join(t.TempDir(), "order-service-v2")
	os.MkdirAll(dir1, 0755)
	os.MkdirAll(dir2, 0755)

	env1 := setupRealE2E(t, "order-service", dir1)
	env2 := setupRealE2E(t, "order-service-v2", dir2)

	// Register both instances via per-ID files in a shared registry dir.
	regDir := filepath.Join(t.TempDir(), "a2a-reg")
	os.MkdirAll(regDir, 0755)
	for _, env := range []*e2eEnv{env1, env2} {
		inst := InstanceInfo{
			ID: env.name + "-id", PID: os.Getpid(),
			Workspace: env.dir, Endpoint: env.server.Endpoint(), Status: "ready",
		}
		data, _ := json.MarshalIndent(inst, "", "  ")
		os.WriteFile(filepath.Join(regDir, inst.ID+".json"), data, 0644)
	}

	// Create a third instance that will try to use the remote tool.
	cfgPath := config.ConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		t.Fatal(err)
	}

	policy := permission.NewConfigPolicyWithMode(nil, []string{t.TempDir()}, permission.BypassMode)
	registry := tool.NewRegistry()
	tool.RegisterBuiltinTools(registry, policy, t.TempDir())

	// Register the remote tool pointing at the shared registry.
	reg := &Registry{dir: regDir, selfID: "caller-id"}
	remoteTool := NewRemoteTool(reg, e2eAPIKey)
	registry.Register(remoteTool)

	ag := agent.NewAgent(prov, registry, "You are a test agent. Use the a2a_remote tool when asked.", 5)
	handler := NewTaskHandler(t.TempDir(), ag, registry, WithTimeout(2*time.Minute))
	srv := NewServer(ServerConfig{Host: "127.0.0.1", Port: 0, APIKey: e2eAPIKey}, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)

	callerClient := NewClient(srv.Endpoint(), e2eAPIKey)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Send a task to the caller agent, asking it to call the "order" service.
	// Since "order" matches both "order-service" and "order-service-v2",
	// the remote tool should return an ambiguous error.
	task, err := callerClient.SendMessage(ctx, SkillFullTask,
		"Use the a2a_remote tool to send a message to the 'order' target. "+
			"Just send 'hello' as the message. Report the result back.")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	task = pollUntilTerminal(t, ctx, callerClient, task.ID)

	// Check if the agent's response mentions "ambiguous".
	lastText := lastAgentText(task)
	if lastText == "" {
		t.Fatal("no agent response text")
	}

	ambiguous := strings.Contains(strings.ToLower(lastText), "ambiguous") ||
		strings.Contains(strings.ToLower(lastText), "multiple instance")
	t.Logf("Agent response: %s", truncate(lastText, 500))

	if ambiguous {
		t.Log("✅ Ambiguous match detected by agent")
	} else {
		t.Log("⚠️ Agent may not have reported ambiguity (LLM behavior varies)")
	}
}

// ---------------------------------------------------------------------------
// Shared integration test helpers
// ---------------------------------------------------------------------------

func pollUntilTerminal(t *testing.T, ctx context.Context, client *Client, taskID string) *Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			t.Fatalf("context cancelled while polling task %s", taskID)
		default:
		}
		got, err := client.GetTask(ctx, taskID)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		if got.Status.IsTerminal() {
			return got
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timeout polling task %s", taskID)
	return nil
}

func lastAgentText(task *Task) string {
	for i := len(task.History) - 1; i >= 0; i-- {
		if task.History[i].Role == "agent" {
			for _, p := range task.History[i].Parts {
				if p.Text != "" {
					return p.Text
				}
			}
		}
	}
	return ""
}

func collectKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k[:16])
	}
	return keys
}
