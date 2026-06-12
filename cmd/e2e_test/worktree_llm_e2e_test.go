//go:build integration_local

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

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
