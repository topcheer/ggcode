package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// ────────────────────────────────────────────────────────
// No-LLM integration tests (pure tool execution)
// ────────────────────────────────────────────────────────

// initE2EGitRepo creates a git repo with an initial commit for worktree testing.
// Uses --no-verify to avoid triggering local hooks.
func initE2EGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %s: %v", name, args, string(out), err)
		}
	}
	run("git", "init")
	run("git", "config", "user.email", "e2e@test.com")
	run("git", "config", "user.name", "E2E Test")

	exec.Command("sh", "-c", "echo 'hello world' > README.md").Run()
	// Create initial file in the correct directory
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello world\n"), 0644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	run("git", "add", "README.md")
	run("git", "commit", "--no-verify", "-m", "initial")

	// Remove hooks to prevent interference
	exec.Command("rm", "-rf", filepath.Join(dir, ".git", "hooks")).Run()
	return dir
}

// TestIntegration_WorktreeCreateAndVerify tests enter_worktree creates a real
// worktree directory with the correct branch, and returns SuggestedWorkingDir.
func TestIntegration_WorktreeCreateAndVerify(t *testing.T) {
	dir := initE2EGitRepo(t)

	enterTool := tool.EnterWorktree{WorkingDir: dir}
	result, err := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"feat-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("enter_worktree failed: %s", result.Content)
	}

	// Verify worktree path (resolve symlinks for macOS /var → /private/var)
	wtPath := filepath.Join(dir, ".ggcode", "worktrees", "feat-test")
	realWant, _ := filepath.EvalSymlinks(wtPath)
	realGot, _ := filepath.EvalSymlinks(result.SuggestedWorkingDir)
	if realGot != realWant {
		t.Errorf("SuggestedWorkingDir = %q, want %q", result.SuggestedWorkingDir, wtPath)
	}

	// Verify branch exists
	cmd := exec.Command("git", "branch", "--list", "feat-test")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "feat-test") {
		t.Errorf("branch 'feat-test' not found: %s", string(out))
	}

	// Verify directory exists and has .git
	info, err := os.Stat(wtPath)
	if err != nil {
		t.Fatalf("worktree directory does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("worktree path is not a directory")
	}

	// Cleanup
	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

// TestIntegration_WorktreeWriteInWorktree tests that after entering a worktree,
// files can be written inside it without affecting the main working tree.
func TestIntegration_WorktreeWriteInWorktree(t *testing.T) {
	dir := initE2EGitRepo(t)

	enterTool := tool.EnterWorktree{WorkingDir: dir}
	result, err := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"write-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("enter_worktree failed: %s", result.Content)
	}

	wtPath := result.SuggestedWorkingDir
	defer exec.Command("git", "worktree", "remove", "--force", wtPath).Run()

	// Write a file inside the worktree using WriteFile tool
	sandbox := func(string) bool { return true }
	writeTool := tool.WriteFile{SandboxCheck: sandbox}

	newFile := filepath.Join(wtPath, "new_feature.go")
	writeResult, err := writeTool.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"path":%q,"content":"package main\n"}`, newFile),
	))
	if err != nil {
		t.Fatal(err)
	}
	if writeResult.IsError {
		t.Fatalf("write_file failed: %s", writeResult.Content)
	}

	// Verify file exists in worktree
	if _, err := os.Stat(newFile); err != nil {
		t.Fatalf("file not created in worktree: %v", err)
	}

	// Verify file does NOT exist in main working tree
	mainFile := filepath.Join(dir, "new_feature.go")
	if _, err := os.Stat(mainFile); !os.IsNotExist(err) {
		t.Error("file should NOT exist in main working tree")
	}

	// Verify git status in worktree shows the new file
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = wtPath
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "new_feature.go") {
		t.Errorf("git status in worktree should show new file: %s", string(out))
	}

	// Verify git status in main repo does NOT show the new file
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, _ = cmd.Output()
	if strings.Contains(string(out), "new_feature.go") {
		t.Errorf("git status in main repo should NOT show worktree file: %s", string(out))
	}
}

// TestIntegration_WorktreeRoundTripRemove tests the full create → write → remove cycle.
func TestIntegration_WorktreeRoundTripRemove(t *testing.T) {
	dir := initE2EGitRepo(t)

	// 1. Enter worktree
	enterTool := tool.EnterWorktree{WorkingDir: dir}
	result, _ := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"roundtrip"}`))
	if result.IsError {
		t.Fatalf("enter failed: %s", result.Content)
	}
	wtPath := result.SuggestedWorkingDir

	// 2. Write a file in the worktree
	sandbox := func(string) bool { return true }
	writeTool := tool.WriteFile{SandboxCheck: sandbox}
	writeTool.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"path":%q,"content":"test"}`, filepath.Join(wtPath, "test.txt")),
	))

	// 3. Exit with remove + discard
	exitTool := tool.ExitWorktree{WorkingDir: wtPath}
	result, _ = exitTool.Execute(context.Background(), json.RawMessage(`{"action":"remove","discard_changes":true}`))
	if result.IsError {
		t.Fatalf("exit remove failed: %s", result.Content)
	}

	// 4. Verify SuggestedWorkingDir points to main repo
	realDir, _ := filepath.EvalSymlinks(dir)
	if result.SuggestedWorkingDir != realDir {
		t.Errorf("SuggestedWorkingDir after remove = %q, want %q", result.SuggestedWorkingDir, realDir)
	}

	// 5. Verify worktree directory is gone
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("worktree directory should be removed")
	}

	// 6. Verify main repo is clean
	cmd := exec.Command("git", "worktree", "list")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if strings.Contains(string(out), "roundtrip") {
		t.Errorf("worktree should not appear in worktree list: %s", string(out))
	}
}

// TestIntegration_WorktreeKeepPreserves tests keep action preserves the worktree.
func TestIntegration_WorktreeKeepPreserves(t *testing.T) {
	dir := initE2EGitRepo(t)

	enterTool := tool.EnterWorktree{WorkingDir: dir}
	result, _ := enterTool.Execute(context.Background(), json.RawMessage(`{"name":"keep-me"}`))
	if result.IsError {
		t.Fatalf("enter failed: %s", result.Content)
	}
	wtPath := result.SuggestedWorkingDir

	// Keep action
	exitTool := tool.ExitWorktree{WorkingDir: wtPath}
	result, _ = exitTool.Execute(context.Background(), json.RawMessage(`{"action":"keep"}`))
	if result.IsError {
		t.Fatalf("exit keep failed: %s", result.Content)
	}

	// Verify worktree still exists
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Error("worktree should still exist after keep")
	}

	// Cleanup
	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

// ────────────────────────────────────────────────────────
// LLM-triggered e2e tests (require real API key)
// ────────────────────────────────────────────────────────

// e2eWorktreeLoop is like e2eBuiltinToolLoop but supports SuggestedWorkingDir.
// It tracks the current working directory and updates it when a tool suggests one.
func e2eWorktreeLoop(ctx context.Context, t *testing.T, prov provider.Provider, reg *tool.Registry, workDir string, userPrompt string) (string, string) {
	t.Helper()
	messages := []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(userPrompt)}},
	}

	defs := reg.ToDefinitions()
	currentDir := workDir

	for round := 0; round < 10; round++ {
		resp, err := prov.Chat(ctx, messages, defs)
		if err != nil {
			t.Fatalf("LLM chat round %d: %v", round, err)
		}

		var toolCalls []provider.ContentBlock
		var textParts []string
		for _, block := range resp.Message.Content {
			switch block.Type {
			case "text":
				textParts = append(textParts, block.Text)
			case "tool_use":
				toolCalls = append(toolCalls, block)
			}
		}

		if len(toolCalls) == 0 {
			return strings.Join(textParts, ""), currentDir
		}

		messages = append(messages, resp.Message)

		for _, tc := range toolCalls {
			tl, ok := reg.Get(tc.ToolName)
			if !ok {
				messages = append(messages, provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{
						provider.ToolResultBlock(tc.ToolID, fmt.Sprintf("unknown tool: %s", tc.ToolName), true),
					},
				})
				continue
			}

			// For worktree tools, inject current working dir
			result, err := tl.Execute(ctx, tc.Input)
			if err != nil {
				messages = append(messages, provider.Message{
					Role: "user",
					Content: []provider.ContentBlock{
						provider.ToolResultBlock(tc.ToolID, fmt.Sprintf("execution error: %v", err), true),
					},
				})
				continue
			}

			// Apply SuggestedWorkingDir (mimics agent behavior)
			if result.SuggestedWorkingDir != "" && !result.IsError {
				t.Logf("WorkingDir changed: %s -> %s (by %s)", currentDir, result.SuggestedWorkingDir, tc.ToolName)
				currentDir = result.SuggestedWorkingDir
			}

			messages = append(messages, provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{
					provider.ToolResultBlock(tc.ToolID, result.Content, result.IsError),
				},
			})
		}
	}

	return "max tool rounds reached", currentDir
}

// TestE2E_WorktreeCreateViaLLM tests that the LLM can create a worktree via tool call.
func TestE2E_WorktreeCreateViaLLM(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := initE2EGitRepo(t)
	defer exec.Command("git", "worktree", "remove", "--force",
		filepath.Join(dir, ".ggcode", "worktrees", "e2e-test")).Run()

	sandbox := func(string) bool { return true }
	reg := tool.NewRegistry()
	reg.Register(tool.EnterWorktree{WorkingDir: dir})
	reg.Register(tool.ExitWorktree{WorkingDir: dir})
	reg.Register(tool.ReadFile{SandboxCheck: sandbox})
	reg.Register(tool.WriteFile{SandboxCheck: sandbox})
	reg.Register(tool.ListDir{SandboxCheck: sandbox})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	prompt := "Use the enter_worktree tool with name 'e2e-test' to create an isolated worktree. " +
		"Then use list_directory to show the contents of the worktree directory. " +
		"Report the worktree path and what you see."

	result, currentDir := e2eWorktreeLoop(ctx, t, prov, reg, dir, prompt)
	t.Logf("Final working dir: %s", currentDir)
	t.Logf("LLM response: %s", result)

	// Verify worktree was created
	wtPath := filepath.Join(dir, ".ggcode", "worktrees", "e2e-test")
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Errorf("worktree was not created at %s", wtPath)
	}
}

// TestE2E_WorktreeWriteAndVerifyViaLLM tests the LLM creates a worktree, writes
// a file inside it, and the file only exists in the worktree.
func TestE2E_WorktreeWriteAndVerifyViaLLM(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := initE2EGitRepo(t)

	sandbox := func(string) bool { return true }
	reg := tool.NewRegistry()
	reg.Register(tool.EnterWorktree{WorkingDir: dir})
	reg.Register(tool.ExitWorktree{WorkingDir: dir})
	reg.Register(tool.ReadFile{SandboxCheck: sandbox})
	reg.Register(tool.WriteFile{SandboxCheck: sandbox})
	reg.Register(tool.ListDir{SandboxCheck: sandbox})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	prompt := "Do the following steps:\n" +
		"1. Use enter_worktree with name 'e2e-write' to create an isolated worktree.\n" +
		"2. Use write_file to create a file called 'experiment.txt' inside the worktree " +
		"with content 'test data from worktree'. Use the path returned by enter_worktree.\n" +
		"3. Use read_file to read the file back and verify its contents.\n" +
		"4. Report the worktree path and whether the file was created successfully."

	result, currentDir := e2eWorktreeLoop(ctx, t, prov, reg, dir, prompt)
	t.Logf("Final working dir: %s", currentDir)
	t.Logf("LLM response: %s", result)

	// Verify the file exists in the worktree
	wtPath := filepath.Join(dir, ".ggcode", "worktrees", "e2e-write")
	expFile := filepath.Join(wtPath, "experiment.txt")
	data, err := os.ReadFile(expFile)
	if err != nil {
		t.Fatalf("experiment.txt not found in worktree: %v", err)
	}
	if !strings.Contains(string(data), "test data from worktree") {
		t.Errorf("file content = %q, want to contain 'test data from worktree'", string(data))
	}

	// Verify the file does NOT exist in main working tree
	mainFile := filepath.Join(dir, "experiment.txt")
	if _, err := os.Stat(mainFile); !os.IsNotExist(err) {
		t.Error("experiment.txt should NOT exist in main working tree")
	}

	// Cleanup
	exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
}

// TestE2E_WorktreeRoundTripViaLLM tests the full create → use → remove lifecycle
// triggered by an LLM prompt.
func TestE2E_WorktreeRoundTripViaLLM(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := initE2EGitRepo(t)

	sandbox := func(string) bool { return true }
	reg := tool.NewRegistry()
	reg.Register(tool.EnterWorktree{WorkingDir: dir})
	reg.Register(tool.ExitWorktree{WorkingDir: dir})
	reg.Register(tool.ReadFile{SandboxCheck: sandbox})
	reg.Register(tool.WriteFile{SandboxCheck: sandbox})
	reg.Register(tool.ListDir{SandboxCheck: sandbox})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	prompt := "Do the following steps in order:\n" +
		"1. Use enter_worktree with name 'e2e-roundtrip' to create an isolated worktree.\n" +
		"2. Use write_file to create 'feature.go' inside the worktree with content 'package feature'.\n" +
		"3. Use exit_worktree with action 'remove' and discard_changes=true to remove the worktree.\n" +
		"4. Report whether the worktree was successfully removed."

	result, currentDir := e2eWorktreeLoop(ctx, t, prov, reg, dir, prompt)
	t.Logf("Final working dir: %s", currentDir)
	t.Logf("LLM response: %s", result)

	// Verify the worktree is removed
	wtPath := filepath.Join(dir, ".ggcode", "worktrees", "e2e-roundtrip")
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed but still exists at %s", wtPath)
		exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
	}
}

// TestE2E_WorktreeViaAgent tests worktree tools through the full agent streaming
// pipeline — the most realistic e2e scenario.
func TestE2E_WorktreeViaAgent(t *testing.T) {
	skipE2E(t)
	prov := newProviderFromEnv(t)
	dir := initE2EGitRepo(t)

	sandbox := func(string) bool { return true }
	reg := tool.NewRegistry()
	reg.Register(tool.EnterWorktree{WorkingDir: dir})
	reg.Register(tool.ExitWorktree{WorkingDir: dir})
	reg.Register(tool.ReadFile{SandboxCheck: sandbox})
	reg.Register(tool.WriteFile{SandboxCheck: sandbox})
	reg.Register(tool.ListDir{SandboxCheck: sandbox})

	a := agent.NewAgent(prov, reg, "You are a helpful assistant. Use the provided tools when asked.", 10)
	a.SetPermissionPolicy(permission.NewConfigPolicyWithMode(nil, []string{"."}, permission.BypassMode))
	a.SetWorkingDir(dir)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var toolResults []string
	err := a.RunStreamWithContent(ctx, []provider.ContentBlock{
		provider.TextBlock("Create a worktree named 'agent-test', write a file called 'hello.txt' with content 'hello from worktree' inside it, then list the directory. Do NOT exit the worktree."),
	}, func(ev provider.StreamEvent) {
		if ev.Type == provider.StreamEventToolResult {
			toolResults = append(toolResults, fmt.Sprintf("%s: error=%v", ev.Tool.Name, ev.IsError))
			t.Logf("  Tool: %s  error=%v  output=%s", ev.Tool.Name, ev.IsError, truncate(ev.Result, 100))
		}
	})
	if err != nil {
		t.Fatalf("RunStreamWithContent error: %v", err)
	}

	t.Logf("Tool results: %v", toolResults)

	// Verify worktree was created
	wtPath := filepath.Join(dir, ".ggcode", "worktrees", "agent-test")
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Errorf("worktree not created at %s", wtPath)
	} else {
		// Verify file was written in worktree
		helloFile := filepath.Join(wtPath, "hello.txt")
		data, err := os.ReadFile(helloFile)
		if err != nil {
			t.Errorf("hello.txt not found: %v", err)
		} else if !strings.Contains(string(data), "hello from worktree") {
			t.Errorf("hello.txt content = %q", string(data))
		}
		// Cleanup
		exec.Command("git", "worktree", "remove", "--force", wtPath).Run()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
