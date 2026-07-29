//go:build darwin || linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
)

// TestSyncVerify_NoCodeChanged verifies that syncVerifyAndGate returns false
// (no action) when no file-editing tools were used in the run.
func TestSyncVerify_NoCodeChanged(t *testing.T) {
	tmp := t.TempDir()
	a := &Agent{
		workingDir:     tmp,
		contextManager: ctxpkg.NewManager(128000),
	}
	stats := newRunStats("test")
	// No file-editing tool calls recorded.

	shouldContinue := a.syncVerifyAndGate(context.Background(), stats, 0)
	if shouldContinue {
		t.Error("expected shouldContinue=false when no code changed")
	}
}

// TestSyncVerify_PassOnFirstTry verifies that a passing verification command
// returns false (proceed to return) and doesn't inject any messages.
func TestSyncVerify_PassOnFirstTry(t *testing.T) {
	tmp := t.TempDir()
	// Create a Makefile with a "test" target that always passes.
	if err := os.WriteFile(filepath.Join(tmp, "Makefile"), []byte("test:\n\techo pass\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		workingDir:     tmp,
		contextManager: ctxpkg.NewManager(128000),
	}
	stats := newRunStats("test")
	stats.recordToolCall("edit_file")

	shouldContinue := a.syncVerifyAndGate(context.Background(), stats, 0)
	if shouldContinue {
		t.Error("expected shouldContinue=false when verification passes")
	}

	// No error message should have been injected.
	msgs := a.contextManager.Messages()
	if len(msgs) > 0 {
		t.Errorf("expected no messages injected, got %d", len(msgs))
	}
}

// TestSyncVerify_FailAndRetry verifies that a failing verification command
// returns true (continue loop) and injects error messages into context.
func TestSyncVerify_FailAndRetry(t *testing.T) {
	tmp := t.TempDir()
	// Create a Makefile with a "test" target that always fails.
	if err := os.WriteFile(filepath.Join(tmp, "Makefile"), []byte("test:\n\tfalse\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		workingDir:     tmp,
		contextManager: ctxpkg.NewManager(128000),
	}
	stats := newRunStats("test")
	stats.recordToolCall("edit_file")

	shouldContinue := a.syncVerifyAndGate(context.Background(), stats, 0)
	if !shouldContinue {
		t.Error("expected shouldContinue=true when verification fails (auto-repair)")
	}

	// Error message should have been injected into context.
	msgs := a.contextManager.Messages()
	if len(msgs) == 0 {
		t.Fatal("expected error message injected into context, got none")
	}
	last := msgs[len(msgs)-1]
	if last.Role != "user" {
		t.Errorf("expected injected message role 'user', got %q", last.Role)
	}
}

// TestSyncVerify_BudgetExhausted verifies that after exceeding the retry
// budget, the method returns false (proceed to return) instead of continuing.
func TestSyncVerify_BudgetExhausted(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "Makefile"), []byte("test:\n\tfalse\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		workingDir:     tmp,
		contextManager: ctxpkg.NewManager(128000),
	}
	stats := newRunStats("test")
	stats.recordToolCall("edit_file")

	// retryCount at the max should return false (budget exhausted).
	shouldContinue := a.syncVerifyAndGate(context.Background(), stats, maxSyncVerifyRetries)
	if shouldContinue {
		t.Error("expected shouldContinue=false when budget exhausted")
	}

	// No error message should be injected when budget exhausted.
	msgs := a.contextManager.Messages()
	if len(msgs) != 0 {
		t.Errorf("expected no messages injected when budget exhausted, got %d", len(msgs))
	}
}

// TestSyncVerify_NoWorkingDir verifies the method returns false when
// workingDir is empty.
func TestSyncVerify_NoWorkingDir(t *testing.T) {
	a := &Agent{
		workingDir:     "",
		contextManager: ctxpkg.NewManager(128000),
	}
	stats := newRunStats("test")
	stats.recordToolCall("edit_file")

	shouldContinue := a.syncVerifyAndGate(context.Background(), stats, 0)
	if shouldContinue {
		t.Error("expected false when workingDir is empty")
	}
}

// TestSyncVerify_NoBuildSystem verifies the method returns false when no
// verification command can be determined (no Makefile, no LLM).
func TestSyncVerify_NoBuildSystem(t *testing.T) {
	tmp := t.TempDir()
	a := &Agent{
		workingDir:     tmp,
		contextManager: ctxpkg.NewManager(128000),
	}
	stats := newRunStats("test")
	stats.recordToolCall("edit_file")

	// No Makefile, no build files, no provider → cmd will be empty.
	shouldContinue := a.syncVerifyAndGate(context.Background(), stats, 0)
	if shouldContinue {
		t.Error("expected false when no verification command available")
	}
}

// TestMaxSyncVerifyRetries_Value ensures the constant is set to a reasonable
// value that allows meaningful auto-repair without excessive token usage.
func TestMaxSyncVerifyRetries_Value(t *testing.T) {
	if maxSyncVerifyRetries < 1 {
		t.Errorf("maxSyncVerifyRetries should be >= 1, got %d", maxSyncVerifyRetries)
	}
	if maxSyncVerifyRetries > 10 {
		t.Errorf("maxSyncVerifyRetries should be <= 10 to avoid token waste, got %d", maxSyncVerifyRetries)
	}
}
