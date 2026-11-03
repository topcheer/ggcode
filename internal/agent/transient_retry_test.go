package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{

		{"timeout", "request timeout after 30s", true},
		{"lsp not ready", "LSP server not ready, please wait", true},
		{"connection refused", "dial tcp: connection refused", true},
		{"rate limit", "rate limit exceeded, try again later", true},
		{"429", "HTTP 429 Too Many Requests", true},
		{"deadline exceeded", "context deadline exceeded", true},
		{"indexing", "language server is indexing files", true},
		{"server crashed", "server crashed unexpectedly", true},
		{"unavailable", "service unavailable", true},

		{"permission denied", "permission denied: cannot access file", false},
		{"unknown tool", "unknown tool: foo. Did you mean bar?", false},
		{"missing param", "missing required parameter: file_path", false},
		{"not found", "file not found: /tmp/missing.go", false},
		{"cancelled", "operation cancelled by user", false},
		{"blocked by hook", "blocked by hook: pre-tool-use", false},
		{"no such file", "open /tmp/missing: no such file or directory", false},
		{"invalid args", "invalid argument: offset must be > 0", false},
		{"sandbox", "path blocked by sandbox policy", false},

		{"permanent priority", "permission denied during timeout", false},

		{"empty", "", false},
		{"unknown error", "something went wrong in the system", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTransientError(tt.content)
			if got != tt.want {
				t.Errorf("isTransientError(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsRetryableTool(t *testing.T) {
	retryable := []string{"read_file", "grep", "lsp_hover", "web_fetch", "git_status", "code_search"}
	for _, name := range retryable {
		if !isRetryableTool(name) {
			t.Errorf("isRetryableTool(%q) = false, want true", name)
		}
	}

	nonRetryable := []string{"edit_file", "write_file", "git_commit", "run_command", "git_add", "git_stash",
		"lsp_rename"}
	for _, name := range nonRetryable {
		if isRetryableTool(name) {
			t.Errorf("isRetryableTool(%q) = true, want false", name)
		}
	}
}

func TestExecuteWithTransientRetry_SuccessOnRetry(t *testing.T) {
	a := newTestAgent(t)
	a.resetTransientRetryBudget()

	var calls int32
	execFn := func(_ context.Context, _ []byte) (tool.Result, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return tool.Result{Content: "LSP server not ready", IsError: true}, nil
		}
		return tool.Result{Content: "success"}, nil
	}

	result := a.executeWithTransientRetry(context.Background(), "lsp_hover", nil, execFn)

	if result.IsError {
		t.Errorf("expected success, got error: %s", result.Content)
	}
	if result.Content != "success" {
		t.Errorf("expected 'success', got %q", result.Content)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (1 fail + 1 success), got %d", calls)
	}
}

func TestExecuteWithTransientRetry_PermanentErrorNoRetry(t *testing.T) {
	a := newTestAgent(t)
	a.resetTransientRetryBudget()

	var calls int32
	execFn := func(_ context.Context, _ []byte) (tool.Result, error) {
		atomic.AddInt32(&calls, 1)
		return tool.Result{Content: "permission denied", IsError: true}, nil
	}

	result := a.executeWithTransientRetry(context.Background(), "read_file", nil, execFn)

	if !result.IsError {
		t.Error("expected error for permanent failure")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry for permanent error), got %d", calls)
	}
}

func TestExecuteWithTransientRetry_NonRetryableToolNoRetry(t *testing.T) {
	a := newTestAgent(t)
	a.resetTransientRetryBudget()

	var calls int32
	execFn := func(_ context.Context, _ []byte) (tool.Result, error) {
		atomic.AddInt32(&calls, 1)
		return tool.Result{Content: "timeout", IsError: true}, nil
	}

	_ = a.executeWithTransientRetry(context.Background(), "edit_file", nil, execFn)

	if calls != 1 {
		t.Errorf("expected 1 call for non-retryable tool, got %d", calls)
	}
}

func TestExecuteWithTransientRetry_AllRetriesFail(t *testing.T) {
	a := newTestAgent(t)
	a.resetTransientRetryBudget()

	var calls int32
	execFn := func(_ context.Context, _ []byte) (tool.Result, error) {
		atomic.AddInt32(&calls, 1)
		return tool.Result{Content: "connection refused", IsError: true}, nil
	}

	result := a.executeWithTransientRetry(context.Background(), "web_fetch", nil, execFn)

	if !result.IsError {
		t.Error("expected error after all retries exhausted")
	}

	if calls != 3 {
		t.Errorf("expected 3 calls (initial + %d retries), got %d", maxTransientRetries, calls)
	}
}

func TestExecuteWithTransientRetry_BudgetExhausted(t *testing.T) {
	a := newTestAgent(t)

	a.mu.Lock()
	a.transientRetryBudget = 0
	a.mu.Unlock()

	var calls int32
	execFn := func(_ context.Context, _ []byte) (tool.Result, error) {
		atomic.AddInt32(&calls, 1)
		return tool.Result{Content: "timeout", IsError: true}, nil
	}

	_ = a.executeWithTransientRetry(context.Background(), "read_file", nil, execFn)

	if calls != 1 {
		t.Errorf("expected 1 call with zero budget, got %d", calls)
	}
}

func TestExecuteWithTransientRetry_CancellationDuringRetry(t *testing.T) {
	a := newTestAgent(t)
	a.resetTransientRetryBudget()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	execFn := func(_ context.Context, _ []byte) (tool.Result, error) {
		return tool.Result{Content: "server crashed", IsError: true}, nil
	}

	result := a.executeWithTransientRetry(ctx, "lsp_hover", nil, execFn)

	if !result.IsError {
		t.Error("expected error on cancellation")
	}
	if result.Content != "cancelled during retry: context canceled" {
		t.Errorf("expected cancellation message, got %q", result.Content)
	}
}

func TestResetTransientRetryBudget(t *testing.T) {
	a := newTestAgent(t)

	a.mu.Lock()
	a.transientRetryBudget = 0
	a.mu.Unlock()

	a.resetTransientRetryBudget()

	a.mu.Lock()
	budget := a.transientRetryBudget
	a.mu.Unlock()

	if budget != maxTransientRetryBudgetPerRun {
		t.Errorf("expected budget %d after reset, got %d", maxTransientRetryBudgetPerRun, budget)
	}
}

func TestExecuteWithTransientRetry_ErrorReturn(t *testing.T) {
	a := newTestAgent(t)
	a.resetTransientRetryBudget()

	var calls int32
	execFn := func(_ context.Context, _ []byte) (tool.Result, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return tool.Result{}, fmt.Errorf("i/o timeout")
		}
		return tool.Result{Content: "ok"}, nil
	}

	result := a.executeWithTransientRetry(context.Background(), "grep", nil, execFn)

	if result.IsError {
		t.Errorf("expected success after retry, got: %s", result.Content)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	return &Agent{}
}

func TestCancellationFlattenNote(t *testing.T) {
	if got := cancellationFlattenNote(context.Canceled); got != " (cancelled - not a tool failure; the run is ending)" {
		t.Fatalf("canceled note mismatch: %q", got)
	}
	if got := cancellationFlattenNote(context.DeadlineExceeded); got != " (deadline exceeded - not a tool failure)" {
		t.Fatalf("deadline note mismatch: %q", got)
	}
	if got := cancellationFlattenNote(errors.New("boom")); got != "" {
		t.Fatalf("ordinary errors must stay unannotated, got %q", got)
	}

	if got := cancellationFlattenNote(fmt.Errorf("exec: %w", context.Canceled)); got == "" {
		t.Fatal("wrapped cancellation must still be annotated")
	}
}
