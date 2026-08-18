package agent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

// Parallel Wait Tool Execution.
//
// When the LLM spawns several sub-agents and then emits multiple wait_agent
// calls in one batch, executing them sequentially means total latency is the
// SUM of each agent's remaining runtime; the second call's live progress is
// invisible in the TUI until the first returns. Executing the wait family
// concurrently makes latency the MAX and streams every agent's progress
// simultaneously.
//
// Safety profile (stricter than read-only pre-execution in parallel_tools.go):
//   - Waiting has no side effects: WaitForSnapshotWithProgress polls sub-agent
//     snapshots; a denied permission merely discards the snapshot.
//   - NO 30s timeout here: waits legitimately run for minutes. Cancellation
//     propagates via ctx (Esc interrupt cancels the whole batch).
//   - Progress callbacks are bound per toolID (same pattern as
//     agent_tool.go), so concurrent waits stream to distinct TUI blocks.
//   - Results are collected into a map keyed by call index and consumed
//     in-order by the sequential loop — the LLM still sees tool_results in
//     call order, preserving the provider API contract.

// parallelWaitTools is the family of blocking-wait tools that are safe to
// execute concurrently within one batch.
var parallelWaitTools = map[string]bool{
	"wait_agent":       true,
	"teammate_results": true,
}

// parallelWaitMaxConcurrent bounds concurrent waits. Waits are I/O-idle
// (polling), so the width can exceed the compute-oriented
// parallelMaxConcurrent=3 used for read-only tools.
const parallelWaitMaxConcurrent = 8

// preExecuteWaitTools identifies wait-family tool calls in the batch and
// executes them concurrently, returning a map from tool-call index to result.
// Returns nil when the batch contains fewer than two wait-family calls (a
// single wait gains nothing from concurrency and should keep the plain
// sequential path).
func (a *Agent) preExecuteWaitTools(ctx context.Context, toolCalls []provider.ToolCallDelta) map[int]preExecutedResult {
	type pending struct {
		index int
		name  string
		args  json.RawMessage
	}
	var batch []pending
	for i, tc := range toolCalls {
		if parallelWaitTools[tc.Name] {
			batch = append(batch, pending{i, tc.Name, tc.Arguments})
		}
	}
	if len(batch) < 2 {
		return nil
	}
	if len(batch) > parallelWaitMaxConcurrent {
		batch = batch[:parallelWaitMaxConcurrent]
	}
	debug.Log("parallel", "pre-executing %d wait-family tools concurrently", len(batch))

	results := make(map[int]preExecutedResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range batch {
		wg.Add(1)
		go func(p pending, tc provider.ToolCallDelta) {
			defer wg.Done()
			defer safego.Recover("agent.parallel.waitPreExec")

			if ctx.Err() != nil {
				return
			}
			t, ok := a.tools.Get(p.name)
			if !ok {
				return
			}

			// Bind the per-call progress callback (toolID-tagged) so
			// concurrent waits stream to distinct progress slots —
			// mirrors agent_tool.go:236-243.
			execCtx := ctx
			if a.onToolProgress != nil {
				fn := a.onToolProgress
				toolID := tc.ID
				execCtx = context.WithValue(execCtx, tool.ToolProgressKey{}, tool.ToolProgressFunc(
					func(_, toolName, output string) {
						fn(toolID, toolName, output)
					},
				))
			}

			start := time.Now()
			result, err := t.Execute(execCtx, p.args)
			dur := time.Since(start)
			if err != nil {
				debug.Log("parallel", "parallel wait pre-exec %s failed: %v (after %v)", p.name, err, dur)
				return
			}

			mu.Lock()
			results[p.index] = preExecutedResult{result, dur}
			mu.Unlock()
			debug.Log("parallel", "wait pre-exec %s done in %v (index=%d)", p.name, dur, p.index)
		}(p, toolCalls[p.index])
	}

	wg.Wait()
	if len(results) == 0 {
		return nil
	}
	debug.Log("parallel", "wait pre-executed %d/%d concurrently", len(results), len(batch))
	return results
}
