package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// Transient tool retry: automatically retry idempotent, read-only tools when
// they fail with transient errors (LSP not ready, network blips, file races).
// This mirrors the retry-on-transient-failure pattern in production agent
// frameworks (LangChain RetryError, AutoGen retry policies). Without auto-retry,
// a single LSP timeout costs a full LLM iteration — the model receives "tool
// timed out", re-issues the same call, and by then the LSP server is ready.
// The retry absorbs that latency silently.

// retryableTools is the set of idempotent, read-only tools that are safe to
// retry on transient errors. Mutating tools (edit_file, write_file, git_commit)
// are NEVER retried automatically — a retry could apply a change twice or
// commit twice. Tools with side effects manage their own retry logic.
var retryableTools = map[string]bool{
	// File reads (idempotent)
	"read_file":       true,
	"multi_file_read": true,
	"list_directory":  true,
	// Search (idempotent)
	"search_files": true,
	"grep":         true,
	"glob":         true,
	"code_search":  true,
	// LSP queries (transient: server may be indexing)
	"lsp_hover":               true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_implementation":      true,
	"lsp_symbols":             true,
	"lsp_workspace_symbols":   true,
	"lsp_diagnostics":         true,
	"lsp_document_highlights": true,
	"lsp_code_actions":        true,
	"lsp_incoming_calls":      true,
	"lsp_outgoing_calls":      true,
	"lsp_rename":              true, // rename is idempotent if input is the same
	// Web (transient: network errors, rate limits)
	"web_fetch":  true,
	"web_search": true,
	// Code health / TODO scan (read-only analysis)
	"code_health": true,
	"scan_todos":  true,
	// Git reads (idempotent)
	"git_status":      true,
	"git_diff":        true,
	"git_log":         true,
	"git_show":        true,
	"git_blame":       true,
	"git_branch_list": true,
	"git_remote":      true,
	"git_stash_list":  true,
	// Debug log (read-only)
	"debug_log": true,
	// Syntax check (read-only)
	"syntax_check": true,
}

// maxTransientRetries is the maximum number of automatic retries for a
// transient failure. With 2 retries, the tool is called up to 3 times total.
const maxTransientRetries = 2

// transientRetryBaseDelay is the base delay for exponential backoff between
// retries. Actual delay: 200ms, then 400ms.
const transientRetryBaseDelay = 200 * time.Millisecond

// maxTransientRetryBudgetPerRun bounds the total retries per agent run,
// preventing retry storms in pathological scenarios.
const maxTransientRetryBudgetPerRun = 8

// transientErrorPatterns are substrings that indicate a transient error.
// Matched case-insensitively against the tool result content.
var transientErrorPatterns = []string{
	// LSP not ready / indexing
	"lsp",
	"language server",
	"server not ready",
	"server crashed",
	"server starting",
	"indexing",
	// Network errors
	"timeout",
	"timed out",
	"deadline exceeded",
	"connection refused",
	"connection reset",
	"no such host",
	"i/o timeout",
	"temporary failure",
	"unexpected eof",
	// File system races
	"resource temporarily unavailable",
	// Rate limiting
	"rate limit",
	"too many requests",
	"429",
	"503",
	// Generic transient
	"try again",
	"unavailable",
}

// permanentErrorPatterns take priority over transient patterns — if an error
// matches both, it's treated as permanent.
var permanentErrorPatterns = []string{
	"permission denied",
	"not permitted",
	"sandbox",
	"unknown tool",
	"did you mean",
	"missing required parameter",
	"invalid argument",
	"malformed",
	"not found",
	"cancelled",
	"blocked by hook",
	"protected path",
	"validation failed",
	"no such file or directory", // permanent for read operations
}

// isTransientError classifies whether a tool error is worth retrying.
func isTransientError(content string) bool {
	lower := strings.ToLower(content)
	if lower == "" {
		return false
	}
	for _, pat := range permanentErrorPatterns {
		if strings.Contains(lower, pat) {
			return false
		}
	}
	for _, pat := range transientErrorPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// isRetryableTool returns true if the tool is idempotent and safe to retry.
func isRetryableTool(toolName string) bool {
	return retryableTools[toolName]
}

// executeWithTransientRetry runs a tool, retrying on transient failures.
// Only idempotent, read-only tools are retried. Mutating tools return after
// a single attempt. The retry budget is decremented per retry across the run.
//
// The execFn parameter is the single-attempt executor (typically calling
// safeExecute). This indirection allows the caller to inject pre/post hooks
// around each attempt.
func (a *Agent) executeWithTransientRetry(
	ctx context.Context,
	toolName string,
	args []byte,
	execFn func(context.Context, []byte) (tool.Result, error),
) tool.Result {
	// Non-retryable tools: single attempt, no retry.
	if !isRetryableTool(toolName) {
		result, err := execFn(ctx, args)
		if err != nil {
			return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
		}
		return result
	}

	// Check retry budget.
	a.mu.Lock()
	budget := a.transientRetryBudget
	a.mu.Unlock()
	if budget <= 0 {
		result, err := execFn(ctx, args)
		if err != nil {
			return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
		}
		return result
	}

	var lastResult tool.Result
	var lastErr error

	for attempt := 0; attempt <= maxTransientRetries; attempt++ {
		if ctx.Err() != nil {
			return tool.Result{Content: fmt.Sprintf("cancelled: %v", ctx.Err()), IsError: true}
		}

		lastResult, lastErr = execFn(ctx, args)

		// Check if retry-worthy.
		shouldRetry := false
		if lastErr != nil {
			shouldRetry = isTransientError(lastErr.Error())
		} else if lastResult.IsError {
			shouldRetry = isTransientError(lastResult.Content)
		}

		if !shouldRetry {
			if lastErr != nil {
				return tool.Result{Content: fmt.Sprintf("tool error: %v", lastErr), IsError: true}
			}
			return lastResult
		}

		// Transient error — decide whether to retry.
		if attempt >= maxTransientRetries {
			errDetail := lastResult.Content
			if lastErr != nil {
				errDetail += lastErr.Error()
			}
			debug.Log("agent", "transient retry exhausted for %s after %d attempts: %s",
				toolName, attempt+1, truncateString(errDetail, 100))
			break
		}

		// Decrement budget and wait with exponential backoff.
		a.mu.Lock()
		a.transientRetryBudget--
		a.mu.Unlock()

		delay := transientRetryBaseDelay * (1 << attempt) // 200ms, 400ms
		debug.Log("agent", "transient retry for %s: attempt %d failed, retrying in %v",
			toolName, attempt+1, delay)

		select {
		case <-time.After(delay):
			continue
		case <-ctx.Done():
			return tool.Result{Content: fmt.Sprintf("cancelled during retry: %v", ctx.Err()), IsError: true}
		}
	}

	// Return the last error/result after retries exhausted.
	if lastErr != nil {
		return tool.Result{Content: fmt.Sprintf("tool error (after %d retries): %v", maxTransientRetries, lastErr), IsError: true}
	}
	return lastResult
}

// resetTransientRetryBudget resets the retry budget at the start of each run.
func (a *Agent) resetTransientRetryBudget() {
	a.mu.Lock()
	a.transientRetryBudget = maxTransientRetryBudgetPerRun
	a.mu.Unlock()
}
