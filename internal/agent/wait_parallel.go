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

var parallelWaitTools = map[string]bool{
	"wait_agent":       true,
	"teammate_results": true,
}

const parallelWaitMaxConcurrent = 8

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
