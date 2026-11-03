//go:build integration_local

package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

const e2eAPIKey = "ggcode-a2a-test-key-2025"

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

func setupRealE2E(t *testing.T, name, workspaceDir string) *e2eEnv {
	t.Helper()

	cfgPath := config.ConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config %s: %v", cfgPath, err)
	}

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("resolve endpoint: %v", err)
	}
	t.Logf("[%s] Using provider: %s / %s / model=%s", name, resolved.VendorName, resolved.EndpointName, resolved.Model)

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	policy := permission.NewConfigPolicyWithMode(nil, []string{workspaceDir}, permission.BypassMode)

	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry, policy, workspaceDir, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	}

	ag := agent.NewAgent(prov, registry, fmt.Sprintf(
		"You are ggcode A2A agent for %s. Execute tasks precisely and concisely.", name,
	), 10)

	handler := NewTaskHandler(workspaceDir, ag, registry,
		WithMaxTasks(3),
		WithTimeout(5*time.Minute),
	)
	srv := NewServer(ServerConfig{
		Host:                  "127.0.0.1",
		Port:                  0,
		APIKey:                e2eAPIKey,
		PushCallbackAllowlist: []string{"push-e2e.invalid"},
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

func setupE2ECluster(t *testing.T) []*e2eEnv {
	t.Helper()
	return []*e2eEnv{
		setupRealE2E(t, "user-service", "/Users/zhanju/ggai/a2a-user-service"),
		setupRealE2E(t, "order-service", "/Users/zhanju/ggai/a2a-order-service"),
		setupRealE2E(t, "gateway", "/Users/zhanju/ggai/a2a-gateway"),
	}
}

func TestRealE2E_DiscoverAndMetadata(t *testing.T) {
	env := setupRealE2E(t, "user-service", "/Users/zhanju/ggai/a2a-user-service")
	ctx := context.Background()

	card, err := env.client.Discover(ctx)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if card.Name != "ggcode" {
		t.Errorf("name = %s, want ggcode", card.Name)
	}
	if len(card.Skills) != 6 {
		t.Errorf("skills = %d, want 6", len(card.Skills))
	}
	if !card.Capabilities.Streaming {
		t.Error("streaming should be true")
	}

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

func TestRealE2E_FileSearch(t *testing.T) {
	env := setupRealE2E(t, "user-service", "/Users/zhanju/ggai/a2a-user-service")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	task, err := env.client.SendMessage(ctx, "file-search", "在 main.go 中搜索所有的 handle 函数名")
	if err != nil {
		t.Fatalf("send task: %v", err)
	}

	t.Logf("Task %s → %s", task.ID, task.Status.State)

	if task.Status.State != TaskStateCompleted {
		for _, a := range task.Artifacts {
			for _, p := range a.Parts {
				t.Logf("  artifact: %s", truncate(p.Text, 200))
			}
		}
		if task.Status.State == TaskStateFailed {
			for _, m := range task.History {
				for _, p := range m.Parts {
					t.Logf("  [%s]: %s", m.Role, truncate(p.Text, 200))
				}
			}
		}
		t.Fatalf("expected completed, got %s", task.Status.State)
	}

	if len(task.Artifacts) == 0 {
		t.Fatal("expected at least 1 artifact")
	}
	result := task.Artifacts[0].Parts[0].Text
	if len(result) == 0 {
		t.Fatal("expected non-empty result")
	}
	if !strings.Contains(result, "main.go") && !strings.Contains(result, "handle") {
		t.Errorf("result should mention main.go or handle functions, got: %s", truncate(result, 300))
	}
	t.Logf("✅ file-search result: %s", truncate(result, 500))
}

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

func TestRealE2E_CodeEdit(t *testing.T) {
	dir := "/Users/zhanju/ggai/a2a-user-service"
	env := setupRealE2E(t, "user-service", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

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
	defer os.Remove(testFile)

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

func TestRealE2E_FullTask(t *testing.T) {
	dir := "/Users/zhanju/ggai/a2a-order-service"
	env := setupRealE2E(t, "order-service", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

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

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "v0.1.0") {
		t.Errorf("expected v0.1.0, got: %s", string(content))
	}
	t.Logf("✅ full-task verified: %s", strings.TrimSpace(string(content)))
}

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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func TestRealE2E_SnapshotIsolation(t *testing.T) {
	env := setupRealE2E(t, "snap-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	task, err := env.client.SendMessage(ctx, SkillFileSearch, "list files in this workspace")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	task = pollUntilTerminal(t, ctx, env.client, task.ID)

	snap1, err := env.client.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap2, err := env.client.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}

	if snap1.Status.State != snap2.Status.State {
		t.Errorf("snapshot states differ: %s vs %s", snap1.Status.State, snap2.Status.State)
	}

	if len(snap1.History) != len(snap2.History) {
		t.Errorf("snapshot history lengths differ: %d vs %d", len(snap1.History), len(snap2.History))
	}

	t.Logf("✅ Snapshot isolation: state=%s, history=%d", snap1.Status.State, len(snap1.History))
}

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

	if task.Status.State == TaskStateWorking || task.Status.State == TaskStateSubmitted {
		t.Errorf("task should be terminal after SendMessage returns, got: %s", task.Status.State)
	}

	t.Logf("✅ Channel notification: task=%s, state=%s", task.ID, task.Status.State)
}

func TestRealE2E_CancelReturnsFreshState(t *testing.T) {
	env := setupRealE2E(t, "cancel-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	warmup, err := env.client.SendMessage(ctx, SkillFileSearch, "list files")
	if err != nil {
		t.Fatalf("warmup: %v", err)
	}
	pollUntilTerminal(t, ctx, env.client, warmup.ID)
	t.Log("warmup task completed ✅")

	streamCh, err := env.client.SendMessageStream(ctx, SkillFullTask,
		"Write a very comprehensive HTTP server in Go with middleware, routing, logging, graceful shutdown, and comprehensive tests. Take your time.")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

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

	time.Sleep(2 * time.Second)
	canceledTask, err := env.client.CancelTask(ctx, taskID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if canceledTask.Status.State != TaskStateCanceled {
		t.Errorf("expected canceled state, got: %s", canceledTask.Status.State)
	}

	got, err := env.client.GetTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.State != TaskStateCanceled {
		t.Errorf("GetTask after cancel: expected canceled, got: %s", got.Status.State)
	}

	t.Logf("✅ Cancel returns fresh state: %s", canceledTask.Status.State)
}

func TestRealE2E_ClientDisconnect(t *testing.T) {
	env := setupRealE2E(t, "disconnect-test", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	_, err := env.client.SendMessage(ctx, SkillFullTask,
		"Write a very detailed REST API implementation in Go with CRUD operations, validation, error handling, and tests. Take your time.")
	elapsed := time.Since(start)

	if elapsed > 10*time.Second {
		t.Errorf("SendMessage took %v — should have returned promptly on ctx cancel", elapsed)
	}

	if err == nil {
		t.Log("task completed within deadline (acceptable)")
	} else {
		t.Logf("✅ Client disconnect: returned in %v with error: %v", elapsed, err)
	}
}

func TestRealE2E_TaskCleanupAfterMultipleRuns(t *testing.T) {
	env := setupRealE2E(t, "cleanup-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

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

	for _, id := range taskIDs {
		_, err := env.client.GetTask(ctx, id)
		if err != nil {
			t.Errorf("task %s should still exist", id[:16])
		}
	}

	env.server.handler.mu.Lock()
	for id := range env.server.handler.tasks {
		tt := env.server.handler.tasks[id]
		if tt.Status.IsTerminal() {
			tt.UpdatedAt = time.Now().Add(-maxCompletedAge - time.Minute)
		}
	}
	env.server.handler.mu.Unlock()

	_, err := env.client.SendMessage(ctx, SkillFileSearch, "list files again")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(500 * time.Millisecond)
	env.server.handler.mu.Lock()
	remaining := len(env.server.handler.tasks)
	env.server.handler.mu.Unlock()

	if remaining > 2 {
		t.Errorf("expected ≤2 tasks remaining after cleanup, got %d", remaining)
	}

	t.Logf("✅ Task cleanup: %d tasks remaining after aging and new submission", remaining)
}

func TestRealE2E_GenerateIDNoCollision(t *testing.T) {
	env := setupRealE2E(t, "id-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

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

func TestRealE2E_TaskEventCallbacks(t *testing.T) {
	env := setupRealE2E(t, "events-test", t.TempDir())

	var events []TaskEventMessage
	var evMu sync.Mutex
	env.server.handler.SetOnTaskEvent(func(msg TaskEventMessage) {
		evMu.Lock()
		events = append(events, msg)
		t.Logf("  event: type=%s task=%s skill=%s", msg.Type, msg.TaskID, msg.Skill)
		evMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := env.client.SendMessage(ctx, SkillFileSearch, "list all files")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	evMu.Lock()
	defer evMu.Unlock()
	if len(events) == 0 {
		t.Fatal("expected at least one event callback")
	}

	if events[0].Type != "start" {
		t.Errorf("expected first event=start, got %s", events[0].Type)
	}
	if events[0].Skill != SkillFileSearch {
		t.Errorf("expected skill=%s, got %s", SkillFileSearch, events[0].Skill)
	}

	foundComplete := false
	for _, e := range events {
		if e.Type == "complete" {
			foundComplete = true
			break
		}
	}
	if !foundComplete {
		t.Errorf("expected complete event, got events: %v", eventTypes(events))
	}

	t.Logf("✅ Event callbacks: %d events, types=%v", len(events), eventTypes(events))
}

func TestRealE2E_CancelEventCallback(t *testing.T) {
	env := setupRealE2E(t, "cancel-event-test", t.TempDir())

	var events []TaskEventMessage
	var evMu sync.Mutex
	env.server.handler.SetOnTaskEvent(func(msg TaskEventMessage) {
		evMu.Lock()
		events = append(events, msg)
		evMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	streamCh, err := env.client.SendMessageStream(ctx, SkillFullTask,
		"Write a comprehensive Go web framework with routing, middleware, templates, and sessions. Be very thorough.")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var taskID string
	select {
	case resp := <-streamCh:
		if resp.Result != nil {
			var m map[string]interface{}
			resultBytes, _ := json.Marshal(resp.Result)
			if json.Unmarshal(resultBytes, &m) == nil {
				if id, ok := m["id"].(string); ok {
					taskID = id
				}
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for first SSE event")
	}
	if taskID == "" {
		t.Fatal("could not get task ID from SSE")
	}

	canceledTask, err := env.client.CancelTask(ctx, taskID)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if canceledTask.Status.State != TaskStateCanceled {
		t.Fatalf("expected canceled, got %s", canceledTask.Status.State)
	}

	time.Sleep(500 * time.Millisecond)

	evMu.Lock()
	defer evMu.Unlock()
	foundCancel := false
	for _, e := range events {
		if e.Type == "cancel" {
			foundCancel = true
			break
		}
	}
	if !foundCancel {
		t.Errorf("expected cancel event, got: %v", eventTypes(events))
	}
	t.Logf("✅ Cancel event: %d events, types=%v", len(events), eventTypes(events))
}

func TestRealE2E_PushNotificationFired(t *testing.T) {
	env := setupRealE2E(t, "push-test", t.TempDir())

	var pushPayloads []StreamResponse
	var pushMu sync.Mutex
	env.server.handler.SetPushNotifier(func(taskID string, payload StreamResponse) {
		pushMu.Lock()
		pushPayloads = append(pushPayloads, payload)
		pushMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	task, err := env.client.SendMessage(ctx, SkillFileSearch, "find any Go file")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	pushMu.Lock()
	defer pushMu.Unlock()
	if len(pushPayloads) == 0 {
		t.Fatal("expected push notification payloads")
	}

	hasNonFinal := false
	hasFinal := false
	for _, p := range pushPayloads {
		if p.StatusUpdate == nil {
			continue
		}
		if p.StatusUpdate.Final {
			hasFinal = true
		} else {
			hasNonFinal = true
		}
	}
	if !hasNonFinal {
		t.Error("expected non-final push (working state)")
	}
	if !hasFinal {
		t.Error("expected final push (completed state)")
	}
	t.Logf("✅ Push notifications: %d payloads, hasNonFinal=%v hasFinal=%v",
		len(pushPayloads), hasNonFinal, hasFinal)
	_ = task
}

func TestRealE2E_AuthNegotiation(t *testing.T) {
	env := setupRealE2E(t, "auth-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	card, err := env.client.Discover(ctx)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if card == nil {
		t.Fatal("expected non-nil card")
	}
	t.Logf("Discovered card: name=%s, skills=%d", card.Name, len(card.Skills))

	if err := env.client.NegotiateAuth(); err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	if env.client.AuthMethod() != "apiKey" {
		t.Errorf("expected apiKey negotiation, got %q", env.client.AuthMethod())
	}

	task, err := env.client.SendMessage(ctx, SkillFileSearch, "list files")
	if err != nil {
		t.Fatalf("send after negotiate: %v", err)
	}
	if task == nil || task.ID == "" {
		t.Error("expected task with ID")
	}
	t.Logf("✅ Auth negotiation: method=%s, task=%s", env.client.AuthMethod(), task.ID)
}

func TestRealE2E_AuthRejectedWithWrongKey(t *testing.T) {
	env := setupRealE2E(t, "auth-reject-test", t.TempDir())

	wrongClient := NewClient(env.server.Endpoint(), "wrong-key-12345")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := wrongClient.SendMessage(ctx, SkillFileSearch, "list files")
	if err == nil {
		t.Fatal("expected error with wrong API key")
	}
	t.Logf("✅ Wrong key rejected: %v", err)
}

func TestRealE2E_PushNotificationConfigCRUD(t *testing.T) {
	env := setupRealE2E(t, "push-crud-test", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pushURL := "https://push-e2e.invalid/callback"

	cfg, err := env.client.SetPushConfig(ctx, PushNotificationConfig{
		TaskID: "e2e-push-crud",
		URL:    pushURL,
		Token:  "test-bearer-token",
	})
	if err != nil {
		t.Fatalf("set push config: %v", err)
	}
	if cfg.ID == "" {
		t.Fatal("expected auto-generated ID")
	}
	t.Logf("Set push config: id=%s url=%s", cfg.ID, cfg.URL)

	result, err := env.client.ListTasks(ctx, "", 10)
	_ = result

	t.Logf("✅ Push CRUD: config set with id=%s", cfg.ID)
}

func eventTypes(events []TaskEventMessage) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}
