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
