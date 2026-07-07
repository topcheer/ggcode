package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

// Parallel Tool Execution — inspired by LLMCompiler (Kim et al., ICML 2024,
// arXiv:2312.04511) and the W&D framework (Lin et al., Salesforce, 2026,
// arXiv:2602.07359).
//
// When the LLM returns multiple tool calls in a single response, independent
// read-only tools can be executed concurrently instead of sequentially.
// LLMCompiler showed 3.7x latency speedup; W&D found 3 parallel tools per
// turn is optimal with 60% fewer turns to completion.
//
// Safety:
//   - Only read-only tools are parallelized (same safe list as speculator)
//   - Permission checks still run in the sequential loop
//   - Post-processing (error-streak, overseer, ratchet) runs sequentially
//   - Only the actual I/O (tool.Execute) is parallelized
//   - Max 3 concurrent goroutines (W&D optimal width)

const (
	// parallelMaxConcurrent bounds the number of concurrent tool executions.
	// W&D (arXiv:2602.07359) found 3 parallel tools per turn optimal.
	parallelMaxConcurrent = 3
)

// preExecutedResult holds a result from parallel pre-execution.
type preExecutedResult struct {
	result   tool.Result
	duration time.Duration
}

// preExecuteReadOnlyTools identifies read-only tool calls in the batch that
// are NOT already in the speculative cache, and executes them concurrently.
// Returns a map from tool call index to result.
//
// This function is safe because:
//  1. Only read-only, idempotent tools are executed (no side effects)
//  2. Permission checks are deferred to the sequential loop
//  3. If permission denies a tool in the sequential loop, the pre-computed
//     result is simply discarded (no harm from executing a read-only tool)
//  4. Context cancellation propagates to all goroutines
//
// Context-fill-aware throttling: when the context window is getting full,
// pre-execution is reduced or skipped to avoid pushing in multiple large
// results simultaneously (research: "parallel tool results arrive in batches,
// potentially pushing context length significantly").
func (a *Agent) preExecuteReadOnlyTools(ctx context.Context, toolCalls []provider.ToolCallDelta) map[int]preExecutedResult {
	if len(toolCalls) <= 1 {
		return nil
	}

	// Context-fill-aware throttling: reduce or skip parallel pre-execution
	// when the context window is under pressure. At 75%+ fill, a batch of
	// 3 large results landing simultaneously could trigger compaction.
	maxConcurrent := parallelMaxConcurrent
	if a.contextManager != nil {
		if threshold := a.contextManager.AutoCompactThreshold(); threshold > 0 {
			fillRatio := float64(a.contextManager.TokenCount()) / float64(threshold)
			switch {
			case fillRatio >= contextFillCritical:
				// Context critically full — skip pre-execution entirely.
				debug.Log("parallel", "skipping pre-execution: context fill %.0f%%", fillRatio*100)
				return nil
			case fillRatio >= contextFillHigh:
				// High fill — reduce to single tool at a time.
				maxConcurrent = 1
				debug.Log("parallel", "reduced pre-execution to 1: context fill %.0f%%", fillRatio*100)
			}
		}
	}

	// Identify which tool calls are read-only and not cached.
	type pending struct {
		index int
		name  string
		args  json.RawMessage
	}
	var batch []pending
	for i, tc := range toolCalls {
		if !speculativeSafeTools[tc.Name] {
			continue
		}
		// Skip if already in speculative cache.
		if a.speculator.hasCached(tc.Name, tc.Arguments) {
			continue
		}
		// Skip if already in memoization cache (avoids redundant execution
		// when the same read-only tool was called earlier in this run).
		if a.toolMemo != nil {
			if _, hit := a.toolMemo.get(tc.Name, tc.Arguments); hit {
				continue
			}
		}
		batch = append(batch, pending{i, tc.Name, tc.Arguments})
	}

	if len(batch) == 0 {
		return nil
	}
	// Cap at maxConcurrent to bound resource usage.
	if len(batch) > maxConcurrent {
		batch = batch[:maxConcurrent]
	}

	results := make(map[int]preExecutedResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range batch {
		wg.Add(1)
		go func(p pending) {
			defer wg.Done()
			defer safego.Recover("agent.parallel.preExec")

			if ctx.Err() != nil {
				return
			}

			t, ok := a.tools.Get(p.name)
			if !ok {
				return
			}

			execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			start := time.Now()
			result, err := t.Execute(execCtx, p.args)
			dur := time.Since(start)

			if err != nil {
				debug.Log("parallel", "parallel pre-exec %s failed: %v (after %v)", p.name, err, dur)
				return
			}

			mu.Lock()
			results[p.index] = preExecutedResult{result, dur}
			mu.Unlock()
			debug.Log("parallel", "pre-executed %s in %v (index=%d)", p.name, dur, p.index)
		}(p)
	}

	wg.Wait()

	if len(results) == 0 {
		return nil
	}
	debug.Log("parallel", "pre-executed %d/%d read-only tools concurrently", len(results), len(batch))
	return results
}

// usePreExecutedWithPermission runs the permission check for a tool and,
// if allowed, returns the pre-executed result. If permission is denied,
// returns a denial message (discarding the pre-executed result safely).
// This mirrors the permission logic in executeToolWithPermission but uses
// the pre-computed result instead of calling executeTool.
func (a *Agent) usePreExecutedWithPermission(ctx context.Context, tc provider.ToolCallDelta, pre preExecutedResult) tool.Result {
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	a.mu.RLock()
	policy := a.policy
	onApproval := a.onApproval
	a.mu.RUnlock()
	if policy != nil {
		decision, err := policy.Check(tc.Name, tc.Arguments)
		if err != nil {
			return tool.Result{Content: fmt.Sprintf("permission check error: %v", err), IsError: true}
		}
		switch decision {
		case permission.Deny:
			return tool.Result{
				Content: fmt.Sprintf("Permission denied for tool %q. The operation was blocked by the permission policy.", tc.Name),
				IsError: true,
			}
		case permission.Ask:
			if onApproval != nil {
				resp := onApproval(ctx, tc.Name, string(tc.Arguments))
				if resp == permission.Deny {
					return tool.Result{
						Content: fmt.Sprintf("Permission denied for tool %q. User rejected the request.", tc.Name),
						IsError: true,
					}
				}
			} else {
				return tool.Result{
					Content: fmt.Sprintf("Permission denied for tool %q. No approval handler available (running in non-interactive mode).", tc.Name),
					IsError: true,
				}
			}
		}
	}

	// Permission allowed — use pre-executed result. Emit metric with actual duration.
	errMsg := ""
	if pre.result.IsError {
		errMsg = truncateString(pre.result.Content, 200)
	}
	a.emitMetric(metrics.MetricEvent{
		Timestamp:    time.Now(),
		Type:         "tool",
		ToolName:     tc.Name,
		ToolSuccess:  !pre.result.IsError,
		ToolError:    errMsg,
		ToolDuration: pre.duration,
	})
	debug.Log("parallel", "using pre-executed result for %s (executed in %v)", tc.Name, pre.duration)
	return pre.result
}
