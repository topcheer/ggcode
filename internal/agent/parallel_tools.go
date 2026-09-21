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

	// Adaptive width (parallel_adaptive.go): combines context-fill pressure,
	// heap-vs-GOMEMLIMIT pressure, a CPU-count cap and the EWMA of recent
	// pre-exec failures into one width decision (0 = skip pre-exec; the
	// sequential loop still runs every call, so skipping is always safe).
	ctxFill := -1.0
	if a.contextManager != nil {
		if threshold := a.contextManager.AutoCompactThreshold(); threshold > 0 {
			ctxFill = float64(a.contextManager.TokenCount()) / float64(threshold)
		}
	}
	maxConcurrent := preExecWidthCtl.width(ctxFill)
	if maxConcurrent <= 0 {
		debug.Log("parallel", "skipping pre-execution (adaptive width=0: ctxFill=%.2f errEMA=%.2f)", ctxFill, preExecWidthCtl.ema())
		return nil
	}

	batch, ok := a.buildPreExecBatch(toolCalls)
	if !ok || len(batch) == 0 {
		return nil
	}
	// Cap at maxConcurrent to bound resource usage.
	if len(batch) > maxConcurrent {
		batch = batch[:maxConcurrent]
	}

	results := make(map[int]preExecutedResult)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var executed, failed int

	for _, p := range batch {
		wg.Add(1)
		go func(p pending) {
			defer wg.Done()
			defer safego.Recover("agent.parallel.preExec")

			result, dur, outcome := a.preExecOne(ctx, p)
			mu.Lock()
			switch outcome {
			case preExecOK:
				results[p.index] = preExecutedResult{result, dur}
				executed++
			case preExecFailed:
				failed++
			}
			mu.Unlock()
			if outcome == preExecOK {
				debug.Log("parallel", "pre-executed %s in %v (index=%d)", p.name, dur, p.index)
			}
		}(p)
	}

	wg.Wait()
	preExecWidthCtl.recordBatch(executed, failed)

	if len(results) == 0 {
		return nil
	}
	debug.Log("parallel", "pre-executed %d/%d read-only tools concurrently", len(results), len(batch))
	return results
}

// pending is one read-only call queued for speculative pre-execution.
type pending struct {
	index int
	name  string
	args  json.RawMessage
}

// buildPreExecBatch selects the calls worth pre-executing. Scheduling guards
// first: #1475-A used to drop ALL pre-execution when ANY mutating tool was
// present - read_file X racing edit_file X handed back pre-edit content
// unlabeled, and the model re-applied the edit. #1590-A/#1607-A/#1649/#1829
// extended that to shell commands, delegate, sub-agent spawns and warp,
// whose write sets are not statically knowable.
//
// Conflict-aware scheduling (parallel_scheduling.go): for file-scoped
// mutators (edit_file/write_file/multi_edit_file/notebook_edit) the write
// target IS knowable, so only reads whose scan scope covers the mutated
// path are withheld; unrelated reads keep their parallel latency win (the
// guard's own asymmetry note: FP = lost parallelism only). Colliding reads
// fall back to the sequential loop, preserving emitted-order semantics
// exactly as the serial path does. Speculative-cache and memoized hits are
// skipped as redundant.
func (a *Agent) buildPreExecBatch(toolCalls []provider.ToolCallDelta) ([]pending, bool) {
	mutatedPaths, schedulable := partitionBatchForParallelism(toolCalls)
	if !schedulable {
		return nil, false
	}
	batch := make([]pending, 0, len(toolCalls))
	for i, tc := range toolCalls {
		if !speculativeSafeTools[tc.Name] {
			continue
		}
		if len(mutatedPaths) > 0 && readAffectedByMutation(tc.Name, tc.Arguments, mutatedPaths) {
			debug.Log("parallel", "withholding %s (index=%d) from pre-exec: collides with pending mutation", tc.Name, i)
			continue
		}
		if a.speculator.hasCached(tc.Name, tc.Arguments) {
			continue
		}
		if a.toolMemo != nil {
			if _, hit := a.toolMemo.get(tc.Name, tc.Arguments); hit {
				continue
			}
		}
		batch = append(batch, pending{index: i, name: tc.Name, args: tc.Arguments})
	}
	return batch, true
}

// preExecOne speculatively executes a single read-only call with a short
// timeout; failures are logged and dropped - the sequential loop re-executes
// them, so a dropped speculative result is never visible to the model.
func (a *Agent) preExecOne(ctx context.Context, p pending) (tool.Result, time.Duration, preExecOutcome) {
	if ctx.Err() != nil {
		return tool.Result{}, 0, preExecCtxCanceled
	}
	t, ok := a.tools.Get(p.name)
	if !ok {
		return tool.Result{}, 0, preExecToolMissing
	}
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	result, err := t.Execute(execCtx, p.args)
	dur := time.Since(start)
	if err != nil {
		debug.Log("parallel", "parallel pre-exec %s failed: %v (after %v)", p.name, err, dur)
		return tool.Result{}, dur, preExecFailed
	}
	return result, dur, preExecOK
}

// preExecOutcome classifies a pre-execution attempt. Only preExecFailed
// feeds the adaptive failure EWMA: cancellation and missing-tool lookups
// are harness state, not environment degradation.
type preExecOutcome int

const (
	preExecOK preExecOutcome = iota
	preExecCtxCanceled
	preExecToolMissing
	preExecFailed
)

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
				Content: a.permissionDeniedMessage(tc.Name),
				IsError: true,
			}
		case permission.Ask:
			// Mode-scoped memory + danger re-check, mirroring agent_tool.go (#1281).
			if a.approvalMemory != nil && policy != nil {
				a.approvalMemory.EnsureModeScope(policy.Mode())
			}
			if a.approvalMemory != nil && a.approvalMemory.ShouldAutoApprove(tc.Name, tc.Arguments) &&
				!(policy != nil && policy.BlocksAutoApprove(tc.Name, tc.Arguments)) {
				debug.Log("approval-memory", "auto-approved %s (learned pattern, parallel)", tc.Name)
				break
			}
			if onApproval != nil {
				resp := onApproval(ctx, tc.Name, string(tc.Arguments))
				if resp == permission.Deny {
					if a.approvalMemory != nil {
						a.approvalMemory.RecordDeny(tc.Name, tc.Arguments)
					}
					return tool.Result{
						Content: fmt.Sprintf("Permission denied for tool %q. User rejected the request.", tc.Name),
						IsError: true,
					}
				}
				if a.approvalMemory != nil {
					a.approvalMemory.RecordApproval(tc.Name, tc.Arguments)
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
