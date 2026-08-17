package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	runtimedebug "runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

// executeToolWithPermission checks the permission policy before executing a tool.
// If the policy returns Ask, the approval handler is consulted interactively.
func (a *Agent) executeToolWithPermission(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}

	// Don't log permission check — permission decision below is sufficient
	a.mu.RLock()
	policy := a.policy
	onApproval := a.onApproval
	a.mu.RUnlock()
	if policy != nil {
		decision, err := policy.Check(tc.Name, tc.Arguments)
		// Only log non-trivial permission decisions (deny/error), not every allow
		if err != nil {
			return tool.Result{Content: fmt.Sprintf("permission check error: %v", err), IsError: true}
		}

		switch decision {
		case permission.Deny:
			return tool.Result{
				Content: fmt.Sprintf("Permission denied for tool %q. The operation was blocked by the permission policy.\n"+
					"To proceed:\n"+
					"1. If this is a file write, ensure the path is within the workspace\n"+
					"2. Ask the user to switch to a more permissive mode (Shift+Tab) or approve the specific operation\n"+
					"3. Do NOT retry the same operation — it will be denied again", tc.Name),
				IsError: true,
			}
		case permission.Ask:
			// Check learned approval memory: if the user has approved this
			// pattern 3+ times, auto-approve to reduce prompt fatigue.
			if a.approvalMemory != nil && a.approvalMemory.ShouldAutoApprove(tc.Name, tc.Arguments) {
				debug.Log("approval-memory", "auto-approved %s (learned pattern)", tc.Name)
				// Fall through to execution — treated as Allow.
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
				// User approved — record for future auto-approval.
				if a.approvalMemory != nil {
					a.approvalMemory.RecordApproval(tc.Name, tc.Arguments)
				}
			} else {
				// No approval handler → deny by default
				return tool.Result{
					Content: fmt.Sprintf("Permission denied for tool %q. No approval handler available (running in non-interactive mode).", tc.Name),
					IsError: true,
				}
			}
		}
	}

	toolStart := time.Now()
	result := a.executeToolWithTimeout(ctx, tc)
	toolDur := time.Since(toolStart)

	// Log slow tool calls for performance debugging
	if toolDur > slowToolThreshold {
		debug.Log("agent", "slow tool: %s took %v", tc.Name, toolDur)
	}

	// Latency baseline outlier detection: warn the agent when a read/search
	// tool call is dramatically slower than its established baseline, so it
	// can self-optimize (narrow scope, use offset/limit, etc.).
	if latencyHint := a.latencyTracker.RecordAndCheck(tc.Name, toolDur); latencyHint != "" {
		if result.Content != "" {
			result.Content = result.Content + "\n\n" + latencyHint
		} else {
			result.Content = latencyHint
		}
	}

	// Tool affinity learning: record outcomes for predictive recommendations (sa-126)
	// Fire tool metric (non-blocking — caller must handle asynchronously).
	errMsg := ""
	if result.IsError {
		errMsg = truncateString(result.Content, 200)
	}
	a.emitMetric(metrics.MetricEvent{
		Timestamp:    time.Now(),
		Type:         "tool",
		ToolName:     tc.Name,
		ToolSuccess:  !result.IsError,
		ToolError:    errMsg,
		ToolDuration: toolDur,
	})
	return result
}

// defaultToolTimeout is the maximum time a single tool call can take when no
// adaptive or category-specific timeout applies. This is the hard ceiling —
// the adaptive system may compute a lower timeout based on tool category and
// historical latency data (see adaptive_timeout.go).
//
// Most tools finish in milliseconds; this catches hung tools (stuck network
// requests, deadlocked processes). Tools that legitimately need more time
// (run_command, start_command) implement their own internal timeouts.
const defaultToolTimeout = 5 * time.Minute

// slowToolThreshold logs a debug warning when a tool takes longer than expected.
const slowToolThreshold = 30 * time.Second

// toolsWithoutTimeout lists tools that manage their own timeout or have
// inherently unbounded execution time. These are NOT wrapped with the
// defaultToolTimeout deadline:
//   - run_command/start_command/etc: implement their own timeout (default 30min)
//   - sleep: explicitly waits for a duration
//   - wait_agent/use_namedagent/delegate: spawn sub-agents that may run for
//     many minutes or hours; they have their own internal lifecycle and timeouts
//   - ask_user: blocks indefinitely waiting for human input
//   - task_output: polls a background task that may still be running
var toolsWithoutTimeout = map[string]bool{
	// Command lifecycle (own timeout)
	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"read_command_output": true,
	"write_command_input": true,
	"stop_command":        true,
	"sleep":               true,
	// Sub-agent / delegation (inherently long-running)
	"wait_agent":       true,
	"use_namedagent":   true,
	"delegate":         true,
	"spawn_agent":      true,
	"task_output":      true,
	"teammate_results": true,
	// Human-in-the-loop (unbounded wait)
	"ask_user": true,
}

// executeToolWithTimeout wraps executeTool with a deadline. If the tool
// exceeds the computed timeout, it cancels the context (to signal the tool
// to abort) and returns a timeout error result.
//
// The timeout is computed adaptively (see adaptive_timeout.go):
//   - Category-based defaults provide sensible bounds per tool type
//   - Historical latency data tightens the timeout once enough samples exist
//   - Hard floor (10s) and ceiling (5min) bounds prevent extremes
func (a *Agent) executeToolWithTimeout(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if toolsWithoutTimeout[tc.Name] {
		return a.executeTool(ctx, tc)
	}

	// Compute the adaptive timeout for this tool.
	timeout := a.latencyTracker.computeAdaptiveTimeout(tc.Name)

	// Create a cancellable sub-context so that on timeout we can signal
	// the tool to abort (tools that check ctx.Done() will exit promptly).
	toolCtx, cancel := context.WithCancel(ctx)

	defer cancel()

	type toolResult struct {
		result tool.Result
	}
	resultCh := make(chan toolResult, 1)

	safego.Go("agent.executeToolWithTimeout", func() {
		defer func() {
			if r := recover(); r != nil {
				resultCh <- toolResult{tool.Result{
					Content: fmt.Sprintf("tool %s panicked: %v", tc.Name, r),
					IsError: true,
				}}
			}
		}()
		resultCh <- toolResult{a.executeTool(toolCtx, tc)}
	})

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		cancel() // signal the tool goroutine to abort
		debug.Log("agent", "tool timeout: %s exceeded %v", tc.Name, timeout)
		return tool.Result{
			Content: fmt.Sprintf("Tool %q timed out after %v. If this is a long-running operation, consider using start_command instead.", tc.Name, timeout),
			IsError: true,
		}
	case r := <-resultCh:
		return r.result
	case <-ctx.Done():
		return tool.Result{Content: fmt.Sprintf("cancelled: %v", ctx.Err()), IsError: true}
	}
}

// executeTool runs pre-hooks, executes the tool, then runs post-hooks.
// File-editing tools (edit_file, write_file) are routed to executeFileTool
// for diff preview and checkpointing.
func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}

	// Inject progress callback for streaming tools (e.g. run_command, wait_command).
	// This is done here (not in executeToolWithTimeout) because tools like
	// run_command and wait_command are in toolsWithoutTimeout and bypass that
	// function entirely. If ToolProgressKey is already set, don't overwrite.
	if a.onToolProgress != nil && ctx.Value(tool.ToolProgressKey{}) == nil {
		fn := a.onToolProgress
		toolID := tc.ID
		ctx = context.WithValue(ctx, tool.ToolProgressKey{}, tool.ToolProgressFunc(
			func(_, toolName, output string) {
				fn(toolID, toolName, output)
			},
		))
	}

	t, ok := a.tools.Get(tc.Name)
	if !ok {
		return tool.Result{Content: tool.FormatUnknownToolError(a.tools, tc.Name), IsError: true}
	}

	// JSON argument repair: many OpenAI-compatible backends (vLLM, LiteLLM,
	// goolm) and weaker models produce arguments that are *almost* valid JSON
	// but fail strict parsing due to stream truncation, trailing commas, smart
	// quotes, or surrounding markdown fences. When this happens, every
	// downstream pre-processor (CoerceArguments, ValidateRequiredParams, etc.)
	// silently bails out on json.Unmarshal failure, and the tool itself fails
	// with a confusing "invalid input" error — wasting a full agent iteration.
	//
	// RepairJSON is a no-op for already-valid JSON (fast path via json.Valid).
	// It is already applied in the OpenAI streaming path (provider layer), but
	// not for other providers (Gemini, Anthropic) or for inline tool calls.
	// Applying it here in the agent pipeline ensures ALL providers benefit.
	if repaired, ok := provider.RepairJSON(tc.Arguments); ok {
		debug.Log("agent", "repaired malformed JSON arguments for tool %s", tc.Name)
		tc.Arguments = repaired
	}

	// Schema-aware argument coercion: weak models (open-weight models via
	// goolm, third-party endpoints) frequently send string values for
	// integer/number/boolean parameters (e.g. {"offset": "50"}). Without
	// coercion this causes a json.Unmarshal type error, wasting a full loop
	// iteration. CoerceArguments is a no-op when args are already well-typed.
	coercedArgs := tool.CoerceArguments(t.Parameters(), tc.Arguments)
	if !bytes.Equal(coercedArgs, tc.Arguments) {
		debug.Log("agent", "coerced arguments for tool %s", tc.Name)
		tc.Arguments = coercedArgs
	}

	// Enum value auto-correction: weak models frequently send near-miss enum
	// values (case mismatch like "JSON" instead of "json", or typos like
	// "overwite" instead of "overwrite"). CoerceEnumValues silently fixes
	// unambiguous corrections, saving a wasted agent loop iteration.
	enumCorrected := tool.CoerceEnumValues(t.Parameters(), tc.Arguments)
	if !bytes.Equal(enumCorrected, tc.Arguments) {
		debug.Log("agent", "enum-corrected arguments for tool %s", tc.Name)
		tc.Arguments = enumCorrected
	}

	// Schema-aware required-parameter validation: catches missing required
	// fields before tool execution, giving the model a clear error message
	// instead of a confusing downstream failure. This complements
	// CoerceArguments (which fixes types, not omissions) and is a no-op for
	// tools that already call CheckRequired internally.
	if missingMsg := tool.ValidateRequiredParams(t.Parameters(), tc.Arguments); missingMsg != "" {
		debug.Log("agent", "required param validation failed for %s: %s", tc.Name, missingMsg)
		return tool.Result{
			Content: fmt.Sprintf("Tool %q: %s. Please provide all required parameters and retry.", tc.Name, missingMsg),
			IsError: true,
		}
	}

	// Schema-constraint validation: catches enum violations, out-of-range
	// numbers, and string length violations before execution. Weak models
	// frequently send invalid enum values (e.g. "xyz" for ["read","write"])
	// or out-of-range offsets. Early detection saves a wasted tool iteration.
	if constraintMsg := tool.ValidateSchemaConstraints(t.Parameters(), tc.Arguments); constraintMsg != "" {
		debug.Log("agent", "schema constraint validation failed for %s: %s", tc.Name, constraintMsg)
		return tool.Result{
			Content: fmt.Sprintf("Tool %q: %s.", tc.Name, constraintMsg),
			IsError: true,
		}
	}

	// Git destructive operation detection: inspect shell commands and git_*
	// tool calls for irreversible operations (reset --hard, force push, clean
	// -fd, rm -rf, etc.). Advisory only — injects a warning but does not block.
	destructiveWarning := a.checkGitDestructive(tc.Name, tc.Arguments)

	// Strip unknown parameters: some models hallucinate extra parameters that
	// aren't in the tool schema. Removing them prevents confusing failures in
	// tools that use strict deserialization. No-op when all params are known.
	strippedArgs := tool.StripUnknownParams(t.Parameters(), tc.Arguments)
	if !bytes.Equal(strippedArgs, tc.Arguments) {
		debug.Log("agent", "stripped unknown params for tool %s", tc.Name)
		tc.Arguments = strippedArgs
	}

	a.mu.RLock()
	hookCfg := a.hookConfig
	workDir := a.workingDir
	a.mu.RUnlock()
	env := hooks.HookEnv{
		ToolName:   tc.Name,
		WorkingDir: workDir,
		FilePath:   hooks.ExtractFilePath(tc.Name, string(tc.Arguments)),
		RawInput:   string(tc.Arguments),
	}

	// Pre-tool-use hooks
	preResult := hooks.RunPreHooks(hookCfg.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	// For file-editing tools: read old content, compute new, show diff, save checkpoint
	if tc.Name == "multi_file_edit" {
		if previewer, ok := t.(interface {
			PreviewChanges(input json.RawMessage) ([]tool.PlannedFileEdit, error)
		}); ok {
			return a.executeMultiFileTool(ctx, t, previewer, tc, env)
		}
	}
	if tc.Name == "edit_file" || tc.Name == "write_file" {
		return a.executeFileTool(ctx, t, tc, env)
	}
	if tc.Name == "undo_edit" {
		return a.executeUndoEdit(ctx, tc)
	}

	// Sync working directory for tools that have a WorkingDir field.
	syncToolWorkingDir(t, workDir)

	a.mu.Lock()
	a.lastTool = t.Name() // Update for next tool
	a.mu.Unlock()

	// Execute the actual tool (with panic recovery + transient retry)
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	toolStart := time.Now()
	result := a.executeWithTransientRetry(ctx, t.Name(), tc.Arguments, func(execCtx context.Context, args []byte) (tool.Result, error) {
		return a.safeExecute(t, execCtx, args)
	})
	toolDur := time.Since(toolStart)

	// Post-tool-use hooks
	postEnv := env
	postEnv.ToolSuccess = !result.IsError
	if result.IsError {
		postEnv.ToolError = truncateString(result.Content, 500)
	}
	postEnv.ToolResult = truncateString(result.Content, 4096)
	postEnv.ToolDuration = toolDur.String()
	postResult := hooks.RunPostHooks(hookCfg.PostToolUse, postEnv)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}

	// Append fallback hint for tool errors - context-aware alternative
	// tool suggestions that save 1-2 wasted agent iterations.
	if result.IsError {
		if hint := toolFallbackHint(t.Name(), result.Content); hint != "" {
			result.Content += hint
		}
	}

	// Append destructive git operation warning (if any was detected pre-execution)
	if destructiveWarning != "" {
		result.Content += destructiveWarning
	}

	// Prompt injection defense: sanitize tool results that may contain
	// adversarial content from external sources (web pages, files, command
	// output). Wraps suspicious content with explicit untrusted-data markers.
	// No-op for file-writing tools and tools that produce self-generated results.
	if !result.IsError {
		result.Content = sanitizeToolResult(tc.Name, result.Content)
	}

	return result
}

func (a *Agent) executeMultiFileTool(ctx context.Context, t tool.Tool, previewer interface {
	PreviewChanges(input json.RawMessage) ([]tool.PlannedFileEdit, error)
}, tc provider.ToolCallDelta, env hooks.HookEnv) tool.Result {
	a.mu.Lock()
	cpMgr := a.checkpoints
	diffFn := a.diffConfirm
	a.mu.Unlock()

	plans, err := previewer.PreviewChanges(tc.Arguments)
	if err == nil && diffFn != nil {
		if diffText, hasChanges := buildMultiFileDiffText(plans); hasChanges {
			label := fmt.Sprintf("%d files", len(plans))
			if len(plans) == 1 {
				label = plans[0].Path
			}
			if !diffFn(ctx, label, diffText) {
				return tool.Result{Content: "Multi-file write cancelled by user.", IsError: true}
			}
		}
	}

	a.mu.RLock()
	hookCfg := a.hookConfig
	a.mu.RUnlock()
	preResult := hooks.RunPreHooks(hookCfg.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	// Pre-write dry-run validation: check all planned edits for fatal errors
	// before any file is written. Blocks the entire batch if any file has a
	// guaranteed-failure condition (syntax error, corruption, etc.).
	if err == nil && len(plans) > 0 {
		planBatch := make([]fileEditPlan, 0, len(plans))
		for _, p := range plans {
			if diff.HasChanges(p.OldContent, p.NewContent) {
				planBatch = append(planBatch, fileEditPlan{
					Path:       p.Path,
					OldContent: p.OldContent,
					NewContent: p.NewContent,
				})
			}
		}
		if blockers := dryRunValidateBatch(planBatch); len(blockers) > 0 {
			var b strings.Builder
			b.WriteString("[Multi-file edit blocked by pre-write validation]\n")
			b.WriteString("One or more files have fatal issues. NO files were modified.\n\n")
			for path, msg := range blockers {
				b.WriteString(fmt.Sprintf("File: %s\n%s\n\n", path, msg))
			}
			return tool.Result{Content: strings.TrimRight(b.String(), "\n"), IsError: true}
		}
	}

	multiStart := time.Now()
	result, err := a.safeExecute(t, ctx, tc.Arguments)
	multiDur := time.Since(multiStart)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	if cpMgr != nil && len(plans) > 0 {
		var outcome tool.MultiFileEditContent
		if err := json.Unmarshal([]byte(result.Content), &outcome); err == nil {
			planByPath := make(map[string]tool.PlannedFileEdit, len(plans))
			for _, plan := range plans {
				planByPath[plan.Path] = plan
			}
			for _, path := range outcome.WrittenPaths {
				if plan, ok := planByPath[path]; ok {
					cpMgr.Save(path, plan.OldContent, plan.NewContent, tc.Name)
				}
			}
		}
	}

	// Post-write integrity check: validate each written file's content.
	if !result.IsError && len(plans) > 0 {
		var integrityWarnings []string
		for _, plan := range plans {
			if diff.HasChanges(plan.OldContent, plan.NewContent) {
				if w := checkWriteIntegrity(plan.Path, plan.OldContent, plan.NewContent); w != "" {
					integrityWarnings = append(integrityWarnings, w)
				}
			}
		}
		if len(integrityWarnings) > 0 {
			combined := strings.Join(integrityWarnings, "\n\n")
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + combined
			} else {
				result.Content = combined
			}
		}
	}

	// Post-write missing test companion detection for multi-file edits.
	if !result.IsError && len(plans) > 0 {
		var testCompanionWarnings []string
		for _, plan := range plans {
			if diff.HasChanges(plan.OldContent, plan.NewContent) {
				if w := CheckMissingTestCompanionWithFS(plan.Path, plan.OldContent, plan.NewContent); w != "" {
					testCompanionWarnings = append(testCompanionWarnings, w)
				}
			}
		}
		if len(testCompanionWarnings) > 0 {
			combined := strings.Join(testCompanionWarnings, "\n\n")
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + combined
			} else {
				result.Content = combined
			}
		}
	}

	// Post-write hardcoded credential detection for multi-file edits
	// (#601 W2): the per-plan checkWriteIntegrity loop above already runs the
	// registry's "hardcoded-secret" check for each file; the direct duplicate
	// call below was removed so each secret surfaces exactly once (the
	// registry copy respects the maxIntegrityWarnings cap).

	// Post-write debug statement detection for multi-file edits.
	if !result.IsError && len(plans) > 0 {
		var debugWarnings []string
		for _, plan := range plans {
			if diff.HasChanges(plan.OldContent, plan.NewContent) {
				if w := checkDebugStmts(plan.Path, plan.OldContent, plan.NewContent); w != "" {
					debugWarnings = append(debugWarnings, w)
				}
			}
		}
		for _, w := range debugWarnings {
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + w
			} else {
				result.Content = w
			}
		}
	}

	postEnv := env
	postEnv.ToolSuccess = !result.IsError
	if result.IsError {
		postEnv.ToolError = truncateString(result.Content, 500)
	}
	postEnv.ToolResult = truncateString(result.Content, 4096)
	postEnv.ToolDuration = multiDur.String()
	postResult := hooks.RunPostHooks(hookCfg.PostToolUse, postEnv)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}

	return result
}

// safeExecute calls t.Execute with panic recovery and context-aware cancellation.
// If the tool panics (e.g. nil pointer dereference from an unset dependency), it returns
// an error result instead of crashing the entire application.
//
// The tool runs in a goroutine. If ctx is cancelled (e.g. user pressed Esc/Ctrl+C),
// safeExecute returns immediately with a cancellation result instead of blocking
// forever on a tool that ignores its context parameter. The goroutine may continue
// running in the background (we can't kill it), but the agent loop is unblocked.
func (a *Agent) safeExecute(t tool.Tool, ctx context.Context, args json.RawMessage) (result tool.Result, err error) {
	// Fill-aware tools (MCP adapter) shrink their own result cap to stay
	// under the context-pressure guard's limits, keeping head-only
	// truncation the single cut (#365, wiring fixed in #369).
	if ft, ok := t.(interface{ SetContextFill(float64) }); ok {
		fill := 0.0
		if threshold := a.contextManager.AutoCompactThreshold(); threshold > 0 {
			fill = float64(a.contextManager.TokenCount()) / float64(threshold)
		}
		ft.SetContextFill(fill)
	}
	type execResult struct {
		result tool.Result
		err    error
	}
	ch := make(chan execResult, 1)
	safego.Go("agent.safeExecute", func() {
		defer func() {
			if r := recover(); r != nil {
				debug.Log("agent", "PANIC recovered in tool %s: %v\n%s", t.Name(), r, runtimedebug.Stack())
				ch <- execResult{tool.Result{
					Content: fmt.Sprintf("tool %s panicked: %v — this is a bug, please report it", t.Name(), r),
					IsError: true,
				}, nil}
			}
		}()
		r, e := t.Execute(ctx, args)
		ch <- execResult{r, e}
	})

	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		debug.Log("agent", "tool %s cancelled via context (Execute did not honor cancellation, goroutine leaked)", t.Name())
		return tool.Result{
			Content: fmt.Sprintf("tool %s was cancelled (it did not respond to cancellation and may still be finishing in the background)", t.Name()),
			IsError: true,
		}, nil
	}
}

// executeFileTool handles edit_file and write_file with diff preview and checkpointing.
func (a *Agent) executeFileTool(ctx context.Context, t tool.Tool, tc provider.ToolCallDelta, env hooks.HookEnv) tool.Result {
	a.mu.Lock()
	cpMgr := a.checkpoints
	diffFn := a.diffConfirm
	a.mu.Unlock()

	// Determine file path and compute old/new content
	filePath, oldContent, newContent, fileExisted, err := a.computeFileChange(tc)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("file change error: %v", err), IsError: true}
	}

	// Pre-write dry-run validation: check proposed content for fatal errors
	// (syntax errors, binary corruption, content loss, conflict markers)
	// BEFORE writing. Blocks the write if a guaranteed-failure condition is
	// detected, saving a full write-detect-recover iteration cycle.
	if diff.HasChanges(oldContent, newContent) {
		if blockMsg := dryRunValidate(filePath, oldContent, newContent); blockMsg != "" {
			return tool.Result{Content: blockMsg, IsError: true}
		}
	}

	// Show diff and ask for confirmation if diffConfirm is set
	if diffFn != nil && diff.HasChanges(oldContent, newContent) {
		diffText := diff.Stats(oldContent, newContent) + "\n" + diff.UnifiedDiff(oldContent, newContent, 3)
		if !diffFn(ctx, filePath, diffText) {
			return tool.Result{Content: fmt.Sprintf("File write to %s cancelled by user.", filePath), IsError: true}
		}
	}

	// Pre-tool-use hooks
	a.mu.RLock()
	hookCfg2 := a.hookConfig
	a.mu.RUnlock()
	preResult := hooks.RunPreHooks(hookCfg2.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	// Execute the actual tool (with panic recovery)
	fileStart := time.Now()
	result, err := a.safeExecute(t, ctx, tc.Arguments)
	fileDur := time.Since(fileStart)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v", err), IsError: true}
	}

	// Save checkpoint. fileExisted distinguishes a file-creating write from an
	// overwrite so undo removes vs restores the file correctly (issue #554 B).
	if cpMgr != nil && !result.IsError {
		cpMgr.SaveWithExistence(filePath, oldContent, newContent, tc.Name, fileExisted)
	}

	// Post-write integrity check: validate file content for syntax errors,
	// binary corruption, or content loss. Catches issues immediately so the
	// agent can fix them in the same turn instead of wasting a build cycle.
	if !result.IsError {
		if warning := checkWriteIntegrity(filePath, oldContent, newContent); warning != "" {
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + warning
			} else {
				result.Content = warning
			}
		}
	}

	// Post-write missing test companion detection: warn when production Go
	// code is written without a corresponding _test.go file. Advisory and
	// non-blocking. Only fires for significant changes (new files with 10+
	// substantive lines, or edits adding new exported functions).
	if !result.IsError {
		if warning := CheckMissingTestCompanionWithFS(filePath, oldContent, newContent); warning != "" {
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + warning
			} else {
				result.Content = warning
			}
		}
	}

	// Post-write hardcoded credential detection (#601 W2): previously this
	// site ran checkHardcodedSecrets directly AND the "hardcoded-secret"
	// registry entry ran it again — same check, same input, two copies of
	// every warning, with the registry copy subject to the
	// maxIntegrityWarnings cap and the direct copy not (behavior drifted with
	// registration order). The registry (wired in #334/#341) is now the
	// single source of truth; the duplicated direct call was removed.

	// Post-write debug statement detection: warn when the agent introduces
	// leftover debug print statements (fmt.Println, console.log, etc.) that
	// are typically added during debugging and forgotten.
	if !result.IsError {
		if debugWarning := checkDebugStmts(filePath, oldContent, newContent); debugWarning != "" {
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + debugWarning
			} else {
				result.Content = debugWarning
			}
		}
	}

	// Post-tool-use hooks
	postEnv := env
	postEnv.ToolSuccess = !result.IsError
	if result.IsError {
		postEnv.ToolError = truncateString(result.Content, 500)
	}
	postEnv.ToolResult = truncateString(result.Content, 4096)
	postEnv.ToolDuration = fileDur.String()
	postResult := hooks.RunPostHooks(hookCfg2.PostToolUse, postEnv)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}

	return result
}

// computeFileChange reads the old content and computes the new content for a file tool call.
// fileExisted (the fourth return value) reports whether the target file existed
// on disk before the tool call; it is false only when write_file creates a new
// file. The checkpoint layer needs it to distinguish "created file" from
// "edited a pre-existing empty file" — OldContent=="" alone cannot (issue #554 B/C).
func (a *Agent) computeFileChange(tc provider.ToolCallDelta) (filePath, oldContent, newContent string, fileExisted bool, err error) {
	fileExisted = true // assume present; write_file on a missing path flips it to false
	switch tc.Name {
	case "edit_file":
		var args struct {
			FilePath   string `json:"file_path"`
			OldText    string `json:"old_text"`
			NewText    string `json:"new_text"`
			ReplaceAll bool   `json:"replace_all"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return "", "", "", false, fmt.Errorf("invalid arguments: %w", err)
		}
		filePath = args.FilePath
		data, err := os.ReadFile(filePath)
		if err != nil {
			// File may not exist yet — that's OK for write_file, but edit_file needs it
			return "", "", "", false, fmt.Errorf("cannot read file: %w", err)
		}
		oldContent = string(data)
		// #601 W1: the simulated edit must mirror the REAL edit_file tool's
		// semantics (replace_all, line-number anchors, fuzzy matching, the
		// uniqueness guard). This simulation feeds dry-run validation, diff
		// preview, and checkpoint snapshots — a divergence lets real writes
		// escape downstream detectors (nil-map-write, sql-injection, ...) and
		// corrupts undo data.
		newContent, err = simulateEditFile(oldContent, args.OldText, args.NewText, args.ReplaceAll)
		if err != nil {
			return "", "", "", false, err
		}
		return filePath, oldContent, newContent, true, nil

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return "", "", "", false, fmt.Errorf("invalid arguments: %w", err)
		}
		filePath = args.Path
		data, err := os.ReadFile(filePath)
		if err != nil {
			// File does not exist yet — this write creates it. Capture that
			// fact so the checkpoint can undo by removing the file instead of
			// writing back an empty buffer (issue #554 B).
			if os.IsNotExist(err) {
				fileExisted = false
				oldContent = ""
			} else {
				return "", "", "", false, fmt.Errorf("cannot read file: %w", err)
			}
		} else {
			oldContent = string(data)
		}
		newContent = args.Content
		return filePath, oldContent, newContent, fileExisted, nil

	default:
		return "", "", "", false, fmt.Errorf("not a file tool: %s", tc.Name)
	}
}

// replaceFirst replaces the first occurrence of old in s with new.
func replaceFirst(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
}

// ---------------------------------------------------------------------------
// #601 W1: edit_file simulation — a faithful port of the real tool's
// replacement semantics (internal/tool/edit_file.go + edit_match.go).
// The sim-prefixed helpers are kept semantically identical to their
// internal/tool counterparts so that simulateEditFile(content, ...) and
// EditFile.Execute(...) produce byte-identical results for the same input.
// internal/tool cannot be reused directly (its matching helpers are
// unexported), and re-deriving simplistic behavior (the old literal
// replaceFirst) is what caused the simulation/real divergence this issue
// fixes. Known limitation: the real tool gofmt's .go files before writing
// (formatGoBytes); already-formatted content is unaffected.
// ---------------------------------------------------------------------------

// simulateEditFile mirrors the core of EditFile.Execute: resolve old_text
// (exact → read_file wrapper strip → line-number anchor → indent/CRLF/
// trailing/fuzzy fallbacks), enforce the uniqueness guard, adjust new_text
// with the same transform, and apply the replacement (all occurrences,
// anchored splice, or first-only). Returns an error exactly where the real
// tool returns an IsError result (empty old_text, no match, non-unique
// match without replace_all) so the pipeline fails the same way.
func simulateEditFile(content, oldText, newText string, replaceAll bool) (string, error) {
	if oldText == "" {
		return "", fmt.Errorf("Error: old_text is required")
	}
	mr := simResolveOldText(content, oldText)
	if mr.canonical == "" {
		return "", fmt.Errorf("old_text not found in file")
	}
	count := strings.Count(content, mr.canonical)
	if !replaceAll && count > 1 && !mr.anchored {
		return "", fmt.Errorf("old_text found %d times in file — must be unique. Add 1-3 lines of surrounding context to disambiguate, copy the exact numbered lines from read_file to anchor the intended occurrence, or set replace_all=true to replace every occurrence", count)
	}
	if mr.transform != "" {
		newText = simAdjustNewText(content, newText, mr)
	}
	if replaceAll {
		return strings.ReplaceAll(content, mr.canonical, newText), nil
	}
	if mr.anchored {
		return content[:mr.start] + newText + content[mr.start+len(mr.canonical):], nil
	}
	return replaceFirst(content, mr.canonical, newText), nil
}

// simMatchResult mirrors tool.matchResult: describes a successful old_text
// resolution against the file content.
type simMatchResult struct {
	canonical string // the actual bytes in content that will be replaced
	transform string // diagnostic tag for which fallback fired ("" = exact)
	shift     string // for leading-indent-shift: prefix to prepend to new_text lines
	trim      string // for leading-indent-shift: prefix to trim from new_text lines
	start     int    // byte offset of canonical in content when the match is anchored
	anchored  bool
}

// simResolveOldText mirrors tool.resolveOldText.
func simResolveOldText(content, oldText string) simMatchResult {
	if oldText == "" {
		return simMatchResult{}
	}
	if strings.Contains(content, oldText) {
		return simMatchResult{canonical: oldText}
	}
	if trimmed, changed := simTrimReadFileWrapperLines(oldText); changed {
		if mr := simResolveOldText(content, trimmed); mr.canonical != "" {
			mr.transform = simPrependTransform("read-file-wrapper-stripped", mr.transform)
			return mr
		}
	}
	if anchored := simTryReadFileLineAnchor(content, oldText); anchored.canonical != "" {
		return anchored
	}
	if normalized := simNormalizeIndentation(content, oldText); normalized != oldText && strings.Contains(content, normalized) {
		return simMatchResult{canonical: normalized, transform: "indent-normalized"}
	}
	if stripped := simStripLineNumberPrefix(oldText); stripped != oldText {
		if strings.Contains(content, stripped) {
			return simMatchResult{canonical: stripped, transform: "line-numbers-stripped"}
		}
		if normalized := simNormalizeIndentation(content, stripped); normalized != stripped && strings.Contains(content, normalized) {
			return simMatchResult{canonical: normalized, transform: "line-numbers-stripped+indent-normalized"}
		}
	}
	if crlf := simTryCRLFMatch(content, oldText); crlf != "" {
		return simMatchResult{canonical: crlf, transform: "crlf-converted"}
	}
	if canonical, shift, trim := simTryLeadingIndentShift(content, oldText); canonical != "" {
		return simMatchResult{canonical: canonical, transform: "leading-indent-shift", shift: shift, trim: trim}
	}
	if trimmed := simTryTrailingWhitespaceMatch(content, oldText); trimmed != "" {
		return simMatchResult{canonical: trimmed, transform: "trailing-whitespace-tolerant"}
	}
	if canonical := simTryFuzzyLineMatch(content, oldText); canonical != "" {
		return simMatchResult{canonical: canonical, transform: "fuzzy-line-match"}
	}
	return simMatchResult{}
}

var (
	simReadFileLineRE           = regexp.MustCompile(`^\s{0,12}(\d+)\t(.*)$`)
	simReadFileLineNumberOnlyRE = regexp.MustCompile(`^\s{0,12}\d+\s*$`)
	simLineNumberPrefixRE       = regexp.MustCompile(`^\s{0,12}\d+\t`)
	simReadFileWrapperLineRE    = regexp.MustCompile(`^(?:\[(?:indent:|encoding:|Extracted from |File truncated:|File has |multi_file_read summary)|=== (?:FILE|ERROR): |\[end (?:file|error)\]$|\[skipped:)`)
)

type simNumberedBlock struct {
	startLine int
	lines     []string
}

type simFileLine struct {
	text  string
	start int
	end   int
}

func simSplitFileLines(content string) []simFileLine {
	if content == "" {
		return nil
	}
	lines := make([]simFileLine, 0, strings.Count(content, "\n")+1)
	start := 0
	for start < len(content) {
		rel := strings.IndexByte(content[start:], '\n')
		if rel < 0 {
			lines = append(lines, simFileLine{
				text:  content[start:],
				start: start,
				end:   len(content),
			})
			break
		}
		end := start + rel
		lines = append(lines, simFileLine{
			text:  content[start:end],
			start: start,
			end:   end,
		})
		start = end + 1
	}
	return lines
}

func simParseReadFileNumberedBlock(text string) (simNumberedBlock, bool) {
	lines := simTrimDanglingReadFileLineNumberOnlyLines(strings.Split(text, "\n"))
	if len(lines) == 0 {
		return simNumberedBlock{}, false
	}
	body := make([]string, len(lines))
	startLine := 0
	for i, line := range lines {
		n, lineText, ok := simParseReadFileLine(line)
		if !ok {
			return simNumberedBlock{}, false
		}
		if i == 0 {
			startLine = n
		} else if n != startLine+i {
			return simNumberedBlock{}, false
		}
		body[i] = lineText
	}
	return simNumberedBlock{startLine: startLine, lines: body}, true
}

func simParseReadFileLine(line string) (lineNumber int, text string, ok bool) {
	if m := simReadFileLineRE.FindStringSubmatch(line); m != nil {
		fmt.Sscanf(m[1], "%d", &lineNumber)
		if lineNumber <= 0 {
			return 0, "", false
		}
		return lineNumber, m[2], true
	}
	if simReadFileLineNumberOnlyRE.MatchString(line) {
		fmt.Sscanf(strings.TrimSpace(line), "%d", &lineNumber)
		if lineNumber <= 0 {
			return 0, "", false
		}
		return lineNumber, "", true
	}
	return 0, "", false
}

func simResolveAnchoredCandidate(content, candidate, oldText string) simMatchResult {
	if candidate == oldText {
		return simMatchResult{canonical: candidate}
	}
	if normalized := simNormalizeIndentation(content, oldText); normalized == candidate {
		return simMatchResult{canonical: candidate, transform: "indent-normalized"}
	}
	if strings.Contains(candidate, "\r\n") && !strings.Contains(oldText, "\r\n") {
		if strings.ReplaceAll(oldText, "\n", "\r\n") == candidate {
			return simMatchResult{canonical: candidate, transform: "crlf-converted"}
		}
	}
	if canonical, shift, trim := simTryLeadingIndentShift(candidate, oldText); canonical == candidate {
		return simMatchResult{canonical: candidate, transform: "leading-indent-shift", shift: shift, trim: trim}
	}
	if trimmed := simTryTrailingWhitespaceMatch(candidate, oldText); trimmed == candidate {
		return simMatchResult{canonical: candidate, transform: "trailing-whitespace-tolerant"}
	}
	return simMatchResult{}
}

func simPrependTransform(prefix, suffix string) string {
	switch {
	case prefix == "":
		return suffix
	case suffix == "":
		return prefix
	default:
		return prefix + "+" + suffix
	}
}

func simTryReadFileLineAnchor(content, oldText string) simMatchResult {
	block, ok := simParseReadFileNumberedBlock(oldText)
	if !ok {
		return simMatchResult{}
	}
	lines := simSplitFileLines(content)
	if block.startLine <= 0 || block.startLine+len(block.lines)-1 > len(lines) {
		return simMatchResult{}
	}
	startIdx := block.startLine - 1
	endIdx := startIdx + len(block.lines) - 1
	candidate := content[lines[startIdx].start:lines[endIdx].end]
	mr := simResolveAnchoredCandidate(content, candidate, strings.Join(block.lines, "\n"))
	if mr.canonical == "" {
		return simMatchResult{}
	}
	mr.canonical = candidate
	mr.transform = simPrependTransform("line-numbers-stripped", mr.transform)
	mr.start = lines[startIdx].start
	mr.anchored = true
	return mr
}

// simAdjustNewText mirrors tool.adjustNewText: applies the transform that
// located old_text to new_text so the replacement stays consistent.
func simAdjustNewText(content, newText string, mr simMatchResult) string {
	out := newText
	if strings.Contains(mr.transform, "read-file-wrapper-stripped") {
		if trimmed, changed := simTrimReadFileWrapperLines(out); changed {
			out = trimmed
		}
	}
	if strings.Contains(mr.transform, "line-numbers-stripped") {
		out = simStripAllLineNumberPrefixes(out)
	}
	if strings.Contains(mr.transform, "indent-normalized") {
		out = simNormalizeIndentation(content, out)
	}
	if strings.Contains(mr.transform, "crlf-converted") && !strings.Contains(out, "\r\n") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	if strings.Contains(mr.transform, "leading-indent-shift") {
		if mr.shift != "" {
			out = simApplyLeadingIndentShift(out, mr.shift)
		}
		if mr.trim != "" {
			out = simTrimLeadingIndentShift(out, mr.trim)
		}
	}
	return out
}

// simStripLineNumberPrefix mirrors tool.stripLineNumberPrefix: removes
// "  42\t" style prefixes if a clear majority of non-empty lines have them.
func simStripLineNumberPrefix(text string) string {
	lines := simTrimDanglingReadFileLineNumberOnlyLines(strings.Split(text, "\n"))
	matched, nonEmpty := 0, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		nonEmpty++
		if simLineNumberPrefixRE.MatchString(l) {
			matched++
		}
	}
	if matched < 2 || matched < (nonEmpty/2+1) {
		return text
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if simReadFileLineNumberOnlyRE.MatchString(l) {
			out[i] = ""
			continue
		}
		out[i] = simLineNumberPrefixRE.ReplaceAllString(l, "")
	}
	return strings.Join(out, "\n")
}

func simStripAllLineNumberPrefixes(text string) string {
	lines := simTrimDanglingReadFileLineNumberOnlyLines(strings.Split(text, "\n"))
	for i, l := range lines {
		if simReadFileLineNumberOnlyRE.MatchString(l) {
			lines[i] = ""
			continue
		}
		lines[i] = simLineNumberPrefixRE.ReplaceAllString(l, "")
	}
	return strings.Join(lines, "\n")
}

func simTrimDanglingReadFileLineNumberOnlyLines(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	hasAnchoredLine := false
	for _, line := range lines {
		if simReadFileLineRE.MatchString(line) || simReadFileLineNumberOnlyRE.MatchString(line) {
			hasAnchoredLine = true
			break
		}
	}
	if !hasAnchoredLine {
		return lines
	}
	start, end := 0, len(lines)
	for start < end && simReadFileLineNumberOnlyRE.MatchString(lines[start]) {
		start++
	}
	for end > start && simReadFileLineNumberOnlyRE.MatchString(lines[end-1]) {
		end--
	}
	return lines[start:end]
}

func simTrimReadFileWrapperLines(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	start, end := 0, len(lines)
	firstContent := start
	for firstContent < end && strings.TrimSpace(lines[firstContent]) == "" {
		firstContent++
	}
	if firstContent < end && simReadFileWrapperLineRE.MatchString(lines[firstContent]) {
		start = firstContent + 1
		for start < end && (strings.TrimSpace(lines[start]) == "" || simReadFileWrapperLineRE.MatchString(lines[start])) {
			start++
		}
	}
	lastContent := end - 1
	for lastContent >= start && strings.TrimSpace(lines[lastContent]) == "" {
		lastContent--
	}
	if lastContent >= start && simReadFileWrapperLineRE.MatchString(lines[lastContent]) {
		end = lastContent
		for end > start && (strings.TrimSpace(lines[end-1]) == "" || simReadFileWrapperLineRE.MatchString(lines[end-1])) {
			end--
		}
	}
	if start == 0 && end == len(lines) {
		return text, false
	}
	trimmed := strings.Join(lines[start:end], "\n")
	if trimmed == "" {
		return text, false
	}
	return trimmed, true
}

// simTryCRLFMatch mirrors tool.tryCRLFMatch.
func simTryCRLFMatch(content, oldText string) string {
	if !strings.Contains(content, "\r\n") {
		return ""
	}
	if strings.Contains(oldText, "\r\n") {
		return ""
	}
	if !strings.Contains(oldText, "\n") {
		return ""
	}
	candidate := strings.ReplaceAll(oldText, "\n", "\r\n")
	if strings.Contains(content, candidate) {
		return candidate
	}
	return ""
}

// simLeadingWhitespace mirrors tool.leadingWhitespace.
func simLeadingWhitespace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

// simTryLeadingIndentShift mirrors tool.tryLeadingIndentShift.
func simTryLeadingIndentShift(content, oldText string) (canonical, shift, trim string) {
	oldLines := strings.Split(oldText, "\n")
	contentLines := strings.Split(content, "\n")
	if len(oldLines) == 0 || len(oldLines) > len(contentLines) {
		return "", "", ""
	}
	stripBoth := func(s string) string { return strings.TrimSpace(s) }
	sOld := make([]string, len(oldLines))
	firstNonEmpty := -1
	for i, l := range oldLines {
		s := stripBoth(l)
		sOld[i] = s
		if s != "" && firstNonEmpty < 0 {
			firstNonEmpty = i
		}
	}
	if firstNonEmpty < 0 {
		return "", "", ""
	}
	for i := 0; i+len(sOld) <= len(contentLines); i++ {
		match := true
		for j := range sOld {
			if stripBoth(contentLines[i+j]) != sOld[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		baseFile := simLeadingWhitespace(contentLines[i+firstNonEmpty])
		baseOld := simLeadingWhitespace(oldLines[firstNonEmpty])
		extraFile, extraOld := "", ""
		switch {
		case strings.HasPrefix(baseFile, baseOld):
			extraFile = baseFile[len(baseOld):]
		case strings.HasPrefix(baseOld, baseFile):
			extraOld = baseOld[len(baseFile):]
		default:
			continue
		}
		if extraFile == "" && extraOld == "" {
			continue
		}
		consistent := true
		for j := range sOld {
			if sOld[j] == "" {
				continue
			}
			fileLead := simLeadingWhitespace(contentLines[i+j])
			oldLead := simLeadingWhitespace(oldLines[j])
			switch {
			case extraFile != "":
				if fileLead != extraFile+oldLead {
					consistent = false
				}
			case extraOld != "":
				if oldLead != extraOld+fileLead {
					consistent = false
				}
			}
			if !consistent {
				break
			}
		}
		if !consistent {
			continue
		}
		return strings.Join(contentLines[i:i+len(sOld)], "\n"), extraFile, extraOld
	}
	return "", "", ""
}

// simApplyLeadingIndentShift mirrors tool.applyLeadingIndentShift.
func simApplyLeadingIndentShift(text, shift string) string {
	if shift == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		lines[i] = shift + l
	}
	return strings.Join(lines, "\n")
}

func simTrimLeadingIndentShift(text, trim string) string {
	if trim == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, trim) {
			lines[i] = l[len(trim):]
		}
	}
	return strings.Join(lines, "\n")
}

// simTryTrailingWhitespaceMatch mirrors tool.tryTrailingWhitespaceMatch.
func simTryTrailingWhitespaceMatch(content, oldText string) string {
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 || len(oldLines) > len(contentLines) {
		return ""
	}
	rstrip := func(s string) string { return strings.TrimRight(s, " \t") }
	rstripOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		rstripOld[i] = rstrip(l)
	}
	rstripContent := make([]string, len(contentLines))
	for i, l := range contentLines {
		rstripContent[i] = rstrip(l)
	}
	for i := 0; i+len(rstripOld) <= len(rstripContent); i++ {
		match := true
		for j := range rstripOld {
			if rstripContent[i+j] != rstripOld[j] {
				match = false
				break
			}
		}
		if match {
			return strings.Join(contentLines[i:i+len(rstripOld)], "\n")
		}
	}
	return ""
}

// simTryFuzzyLineMatch mirrors tool.tryFuzzyLineMatch.
func simTryFuzzyLineMatch(content, oldText string) string {
	oldLines := strings.Split(oldText, "\n")
	if len(oldLines) == 0 {
		return ""
	}
	trimmedOld := make([]string, len(oldLines))
	for i, l := range oldLines {
		trimmedOld[i] = strings.TrimSpace(l)
	}
	fileLines := strings.Split(content, "\n")
	nFile := len(fileLines)
	nOld := len(trimmedOld)
	for start := 0; start <= nFile-nOld; start++ {
		matched := true
		for j := 0; j < nOld; j++ {
			if strings.TrimSpace(fileLines[start+j]) != trimmedOld[j] {
				matched = false
				break
			}
		}
		if matched {
			return strings.Join(fileLines[start:start+nOld], "\n")
		}
	}
	return ""
}

// simNormalizeIndentation mirrors tool.normalizeIndentation: converts the
// indentation of text to match the file's tab/space style.
func simNormalizeIndentation(fileContent, text string) string {
	fileUsesTabs := false
	fileTabWidth := 0
	{
		tabLines, spaceLines := 0, 0
		spaceWidths := map[int]int{}
		lines := strings.Split(fileContent, "\n")
		limit := len(lines)
		if limit > 200 {
			limit = 200
		}
		for _, line := range lines[:limit] {
			if len(line) == 0 {
				continue
			}
			if line[0] == '\t' {
				tabLines++
			} else if line[0] == ' ' {
				spaceLines++
				n := 0
				for n < len(line) && line[n] == ' ' {
					n++
				}
				if n >= 2 {
					spaceWidths[n]++
				}
			}
		}
		fileUsesTabs = tabLines > spaceLines
		if len(spaceWidths) > 0 {
			allW := make([]int, 0, len(spaceWidths))
			for w := range spaceWidths {
				allW = append(allW, w)
			}
			g := allW[0]
			for _, w := range allW[1:] {
				g = simGCD(g, w)
			}
			if g < 2 {
				g = 2
			}
			fileTabWidth = g
		}
		if fileTabWidth == 0 {
			fileTabWidth = 4
		}
	}
	textHasTabs := false
	textHasLeadingSpaces := false
	for _, line := range strings.Split(text, "\n") {
		if len(line) > 0 && line[0] == '\t' {
			textHasTabs = true
		}
		if len(line) > 0 && line[0] == ' ' {
			textHasLeadingSpaces = true
		}
	}
	if fileUsesTabs && !textHasTabs && textHasLeadingSpaces {
		return simConvertSpacesToTabs(text, fileTabWidth)
	}
	if !fileUsesTabs && textHasTabs {
		return simConvertTabsToSpaces(text, fileTabWidth)
	}
	return text
}

func simConvertSpacesToTabs(text string, tabWidth int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		spaces := 0
		for spaces < len(line) && line[spaces] == ' ' {
			spaces++
		}
		if spaces == 0 {
			continue
		}
		tabs := spaces / tabWidth
		remainder := spaces % tabWidth
		if tabs == 0 && spaces > 0 {
			tabs = 1
			remainder = 0
		}
		lines[i] = strings.Repeat("\t", tabs) + strings.Repeat(" ", remainder) + line[spaces:]
	}
	return strings.Join(lines, "\n")
}

func simConvertTabsToSpaces(text string, tabWidth int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		tabs := 0
		for tabs < len(line) && line[tabs] == '\t' {
			tabs++
		}
		if tabs == 0 {
			continue
		}
		lines[i] = strings.Repeat(" ", tabs*tabWidth) + line[tabs:]
	}
	return strings.Join(lines, "\n")
}

func simGCD(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// executeUndoEdit handles the undo_edit tool by routing to the checkpoint manager.
// Supports three actions: "undo" (revert last edit), "list" (show checkpoints),
// and "revert" (roll back to a specific checkpoint by ID).
func (a *Agent) executeUndoEdit(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	a.mu.Lock()
	cpMgr := a.checkpoints
	a.mu.Unlock()

	if cpMgr == nil {
		return tool.Result{
			IsError: true,
			Content: "Checkpoint system is not initialized. Cannot undo edits.",
		}
	}

	var args struct {
		Action       string `json:"action"`
		CheckpointID string `json:"checkpoint_id"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil {
		return tool.Result{Content: fmt.Sprintf("invalid input: %v", err), IsError: true}
	}
	if args.Action == "" {
		args.Action = "undo"
	}

	switch args.Action {
	case "undo":
		cp, err := cpMgr.Undo()
		if err != nil {
			return tool.Result{
				IsError: true,
				Content: fmt.Sprintf("Nothing to undo: %v", err),
			}
		}
		// Existed (not OldContent=="") distinguishes created files from
		// pre-existing empty files (issue #554 B/C). With Existed=false the
		// manager has already REMOVED the file, so the report must say so —
		// claiming "restored" would mislead the agent about disk state.
		isNew := !cp.Existed
		result := tool.FormatUndoResult(cp.FilePath, cp.ToolCall, isNew)
		// Include a diff summary of what changed
		if !isNew && diff.HasChanges(cp.NewContent, cp.OldContent) {
			result += "\n\n" + diff.Stats(cp.NewContent, cp.OldContent)
		}
		debug.Log("agent", "undo_edit: reverted %s (was %s)", cp.FilePath, cp.ToolCall)
		return tool.Result{Content: result}

	case "list":
		cps := cpMgr.List()
		infos := make([]tool.CheckpointInfo, len(cps))
		for i, cp := range cps {
			infos[i] = tool.CheckpointInfo{
				ID:        cp.ID,
				FilePath:  cp.FilePath,
				ToolCall:  cp.ToolCall,
				Timestamp: cp.Timestamp,
				IsNew:     !cp.Existed,
			}
		}
		return tool.Result{Content: tool.FormatCheckpointList(infos)}

	case "revert":
		if args.CheckpointID == "" {
			return tool.Result{
				IsError: true,
				Content: "checkpoint_id is required for action=revert. Use action=list to see available checkpoint IDs.",
			}
		}
		cp, err := cpMgr.Revert(args.CheckpointID)
		if err != nil {
			return tool.Result{
				IsError: true,
				Content: fmt.Sprintf("Revert failed: %v", err),
			}
		}
		isNew := !cp.Existed // not OldContent=="": pre-existing empty files are edits, not creations (#554 C)
		result := tool.FormatUndoResult(cp.FilePath, cp.ToolCall, isNew)
		result += fmt.Sprintf("\n\nAll edits after checkpoint %s have also been reverted.", args.CheckpointID)
		debug.Log("agent", "undo_edit: reverted to %s for %s", args.CheckpointID, cp.FilePath)
		return tool.Result{Content: result}

	default:
		return tool.Result{
			IsError: true,
			Content: fmt.Sprintf("Unknown action %q. Supported: undo, list, revert.", args.Action),
		}
	}
}

func buildMultiFileDiffText(plans []tool.PlannedFileEdit) (string, bool) {
	var out strings.Builder
	hasChanges := false
	for _, plan := range plans {
		if !diff.HasChanges(plan.OldContent, plan.NewContent) {
			continue
		}
		if hasChanges {
			out.WriteString("\n")
		}
		out.WriteString("=== ")
		out.WriteString(plan.Path)
		out.WriteString(" ===\n")
		out.WriteString(diff.Stats(plan.OldContent, plan.NewContent) + "\n")
		out.WriteString(diff.UnifiedDiff(plan.OldContent, plan.NewContent, 3))
		hasChanges = true
	}
	return out.String(), hasChanges
}

// toolWorkingDirMu is a safety-net mutex for syncToolWorkingDir. With Registry.Clone(),
// each agent has its own tool instances and this mutex should never be contended.
// It exists as a last resort in case a tool without a Clone() implementation is
// shared between agents and has a WorkingDir field that needs mutation.
var toolWorkingDirMu sync.Mutex

// syncToolWorkingDir uses reflection to set the WorkingDir field on tools
// that have one. This ensures tools always use the agent's current working
// directory, even after it changes (e.g., after enter_worktree).
//
// Note: With Registry.Clone(), each agent has independent tool instances, so
// this reflection is only mutating per-agent copies. The mutex is a safety net.
func syncToolWorkingDir(t tool.Tool, dir string) {
	toolWorkingDirMu.Lock()
	defer toolWorkingDirMu.Unlock()

	// Dereference pointer if needed
	v := reflect.ValueOf(t)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	f := v.FieldByName("WorkingDir")
	if f.IsValid() && f.CanSet() && f.Kind() == reflect.String {
		f.SetString(dir)
	}
}

// truncateString truncates s to at most maxLen runes, appending "..." if truncated.
// Uses rune-based truncation to avoid breaking multi-byte UTF-8 characters.
func truncateString(s string, maxLen int) string {
	if maxLen < 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}
