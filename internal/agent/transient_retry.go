package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

var retryableTools = map[string]bool{

	"read_file":       true,
	"multi_file_read": true,
	"list_directory":  true,

	"search_files": true,
	"grep":         true,
	"glob":         true,
	"code_search":  true,

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

	"web_fetch":  true,
	"web_search": true,

	"code_health": true,
	"scan_todos":  true,

	"git_status":      true,
	"git_diff":        true,
	"git_log":         true,
	"git_show":        true,
	"git_blame":       true,
	"git_branch_list": true,
	"git_remote":      true,
	"git_stash_list":  true,

	"debug_log": true,

	"syntax_check": true,
}

const maxTransientRetries = 2

const transientRetryBaseDelay = 200 * time.Millisecond

const maxTransientRetryBudgetPerRun = 8

var transientErrorPatterns = []string{

	"lsp",
	"language server",
	"server not ready",
	"server crashed",
	"server starting",
	"indexing",

	"timeout",
	"timed out",
	"deadline exceeded",
	"connection refused",
	"connection reset",
	"no such host",
	"i/o timeout",
	"temporary failure",
	"unexpected eof",

	"resource temporarily unavailable",

	"rate limit",
	"too many requests",
	"429",
	"503",

	"try again",
	"unavailable",
}

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
	"no such file or directory",
}

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

func isRetryableTool(toolName string) bool {
	return retryableTools[toolName]
}

func cancellationFlattenNote(err error) string {
	if errors.Is(err, context.Canceled) {
		return " (cancelled - not a tool failure; the run is ending)"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return " (deadline exceeded - not a tool failure)"
	}
	return ""
}

func (a *Agent) executeWithTransientRetry(
	ctx context.Context,
	toolName string,
	args []byte,
	execFn func(context.Context, []byte) (tool.Result, error),
) tool.Result {

	if !isRetryableTool(toolName) {
		result, err := execFn(ctx, args)
		if err != nil {
			return tool.Result{Content: fmt.Sprintf("tool error: %v%s", err, cancellationFlattenNote(err)), IsError: true}
		}
		return result
	}

	a.mu.Lock()
	budget := a.transientRetryBudget
	a.mu.Unlock()
	if budget <= 0 {
		result, err := execFn(ctx, args)
		if err != nil {
			return tool.Result{Content: fmt.Sprintf("tool error: %v%s", err, cancellationFlattenNote(err)), IsError: true}
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

		shouldRetry := false
		if lastErr != nil {
			shouldRetry = isTransientError(lastErr.Error())
		} else if lastResult.IsError {
			shouldRetry = isTransientError(lastResult.Content)
		}

		if !shouldRetry {
			if lastErr != nil {
				return tool.Result{Content: fmt.Sprintf("tool error: %v%s", lastErr, cancellationFlattenNote(lastErr)), IsError: true}
			}
			return lastResult
		}

		if attempt >= maxTransientRetries {
			errDetail := lastResult.Content
			if lastErr != nil {
				errDetail += lastErr.Error()
			}
			debug.Log("agent", "transient retry exhausted for %s after %d attempts: %s",
				toolName, attempt+1, truncateString(errDetail, 100))
			break
		}

		a.mu.Lock()
		a.transientRetryBudget--
		a.mu.Unlock()

		delay := transientRetryBaseDelay * (1 << attempt)
		debug.Log("agent", "transient retry for %s: attempt %d failed, retrying in %v",
			toolName, attempt+1, delay)

		select {
		case <-time.After(delay):
			continue
		case <-ctx.Done():
			return tool.Result{Content: fmt.Sprintf("cancelled during retry: %v", ctx.Err()), IsError: true}
		}
	}

	if lastErr != nil {
		return tool.Result{Content: fmt.Sprintf("tool error (after %d retries): %v", maxTransientRetries, lastErr), IsError: true}
	}
	return lastResult
}

func (a *Agent) resetTransientRetryBudget() {
	a.mu.Lock()
	a.transientRetryBudget = maxTransientRetryBudgetPerRun
	a.mu.Unlock()
}
