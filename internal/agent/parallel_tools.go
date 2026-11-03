package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	parallelMaxConcurrent = 3
)

type preExecutedResult struct {
	result   tool.Result
	duration time.Duration
}

func (a *Agent) preExecuteReadOnlyTools(ctx context.Context, toolCalls []provider.ToolCallDelta) map[int]preExecutedResult {
	if len(toolCalls) <= 1 {
		return nil
	}

	maxConcurrent := parallelMaxConcurrent
	if a.contextManager != nil {
		if threshold := a.contextManager.AutoCompactThreshold(); threshold > 0 {
			fillRatio := float64(a.contextManager.TokenCount()) / float64(threshold)
			switch {
			case fillRatio >= contextFillCritical:

				debug.Log("parallel", "skipping pre-execution: context fill %.0f%%", fillRatio*100)
				return nil
			case fillRatio >= contextFillHigh:

				maxConcurrent = 1
				debug.Log("parallel", "reduced pre-execution to 1: context fill %.0f%%", fillRatio*100)
			}
		}
	}

	batch, ok := a.buildPreExecBatch(toolCalls)
	if !ok || len(batch) == 0 {
		return nil
	}

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

			result, dur, ok := a.preExecOne(ctx, p)
			if !ok {
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

type pending struct {
	index int
	name  string
	args  json.RawMessage
}

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

func (a *Agent) preExecOne(ctx context.Context, p pending) (tool.Result, time.Duration, bool) {
	if ctx.Err() != nil {
		return tool.Result{}, 0, false
	}
	t, ok := a.tools.Get(p.name)
	if !ok {
		return tool.Result{}, 0, false
	}
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	result, err := t.Execute(execCtx, p.args)
	dur := time.Since(start)
	if err != nil {
		debug.Log("parallel", "parallel pre-exec %s failed: %v (after %v)", p.name, err, dur)
		return tool.Result{}, dur, false
	}
	return result, dur, true
}

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
			a.auditToolResult(tc.Name, tc.Arguments, audit.StatusPolicyDenied,
				"denied by permission policy (mode-independent rule, parallel)", 0, "")
			return tool.Result{
				Content: a.permissionDeniedMessage(tc.Name),
				IsError: true,
			}
		case permission.Ask:

			if a.approvalMemory != nil && policy != nil {
				a.approvalMemory.EnsureModeScope(policy.Mode())
			}
			if a.approvalMemory != nil && a.approvalMemory.ShouldAutoApprove(tc.Name, tc.Arguments) &&
				!(policy != nil && policy.BlocksAutoApprove(tc.Name, tc.Arguments)) {
				debug.Log("approval-memory", "auto-approved %s (learned pattern, parallel)", tc.Name)
				break
			}
			if onApproval != nil {

				if a.askThrottle.ShouldSuppress(tc.Name, tc.Arguments) {
					a.auditToolResult(tc.Name, tc.Arguments, audit.StatusUserDenied, "suppressed by ask throttle (repeated denial, parallel)", 0, "")
					debug.Log("approval-throttle", "suppressed re-ask for %s (parallel, denied twice recently)", tc.Name)
					return tool.Result{
						Content: fmt.Sprintf("Permission denied for tool %q without prompting: this exact request was denied twice within the last minute. Re-asking is blocked to avoid nagging the user. Change your approach instead of repeating the request.", tc.Name),
						IsError: true,
					}
				}
				resp := onApproval(ctx, tc.Name, string(tc.Arguments))
				if resp == permission.Deny {

					a.auditToolResult(tc.Name, tc.Arguments, audit.StatusUserDenied, "user denied at approval gate (parallel)", 0, "")
					if a.approvalMemory != nil {
						a.approvalMemory.RecordDeny(tc.Name, tc.Arguments)
					}
					a.askThrottle.RecordDenial(tc.Name, tc.Arguments)
					return tool.Result{
						Content: fmt.Sprintf("Permission denied for tool %q. User rejected the request.", tc.Name),
						IsError: true,
					}
				}
				if resp.IsNonDecision() {

					note := "approval timed out (no user response, parallel)"
					if resp == permission.Cancelled {
						note = "approval cancelled (run interrupted or request displaced, parallel)"
					}
					a.auditToolResult(tc.Name, tc.Arguments, audit.StatusAskTimeout, note, 0, "")
					debug.Log("approval-gate", "ask ended without user decision for %s (parallel, %v)", tc.Name, resp)
					return tool.Result{
						Content: fmt.Sprintf("Approval for tool %q ended without a user decision (%s). The tool was not executed. If this step is still needed, ask again when the user is available; do not treat this as a rejection of the approach.", tc.Name, resp),
						IsError: true,
					}
				}
				a.auditToolResult(tc.Name, tc.Arguments, audit.StatusUserApproved, "", 0, "")
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
