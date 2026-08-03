package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
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

	go func() {
		defer func() {
			if r := recover(); r != nil {
				resultCh <- toolResult{tool.Result{
					Content: fmt.Sprintf("tool %s panicked: %v", tc.Name, r),
					IsError: true,
				}}
			}
		}()
		resultCh <- toolResult{a.executeTool(toolCtx, tc)}
	}()

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

	// Post-write hardcoded credential detection for multi-file edits.
	if !result.IsError && len(plans) > 0 {
		var secretWarnings []string
		for _, plan := range plans {
			if diff.HasChanges(plan.OldContent, plan.NewContent) {
				secretWarnings = append(secretWarnings, checkHardcodedSecrets(plan.Path, plan.OldContent, plan.NewContent)...)
			}
		}
		for _, w := range secretWarnings {
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + w
			} else {
				result.Content = w
			}
		}
	}

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

	// Post-write auto-format: run formatters on all successfully written files.
	if !result.IsError {
		for _, plan := range plans {
			if diff.HasChanges(plan.OldContent, plan.NewContent) {
				if formatNotice := autoFormatFile(plan.Path); formatNotice != "" {
					if result.Content != "" {
						result.Content = result.Content + "\n\n" + formatNotice
					} else {
						result.Content = formatNotice
					}
				}
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
	type execResult struct {
		result tool.Result
		err    error
	}
	ch := make(chan execResult, 1)
	go func() {
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
	}()

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
	filePath, oldContent, newContent, err := a.computeFileChange(tc)
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

	// Save checkpoint
	if cpMgr != nil && !result.IsError {
		cpMgr.Save(filePath, oldContent, newContent, tc.Name)
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

	// Post-write hardcoded credential detection: warn when the agent introduces
	// real credential patterns (AWS keys, GitHub tokens, private keys, etc.) into
	// source files. This prevents the agent from creating security vulnerabilities.
	// Uses delta-based detection: only flags secrets INTRODUCED by this edit.
	if !result.IsError {
		secretWarnings := checkHardcodedSecrets(filePath, oldContent, newContent)
		for _, w := range secretWarnings {
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + w
			} else {
				result.Content = w
			}
		}
	}

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

	// Post-write auto-format: run the language-appropriate formatter
	// (gofmt, goimports, prettier, rustfmt, etc.) on the file after a
	// successful write. Silently skips if the formatter is not installed.
	if !result.IsError {
		if formatNotice := autoFormatFile(filePath); formatNotice != "" {
			if result.Content != "" {
				result.Content = result.Content + "\n\n" + formatNotice
			} else {
				result.Content = formatNotice
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
func (a *Agent) computeFileChange(tc provider.ToolCallDelta) (filePath, oldContent, newContent string, err error) {
	switch tc.Name {
	case "edit_file":
		var args struct {
			FilePath string `json:"file_path"`
			OldText  string `json:"old_text"`
			NewText  string `json:"new_text"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return "", "", "", fmt.Errorf("invalid arguments: %w", err)
		}
		filePath = args.FilePath
		data, err := os.ReadFile(filePath)
		if err != nil {
			// File may not exist yet — that's OK for write_file, but edit_file needs it
			return "", "", "", fmt.Errorf("cannot read file: %w", err)
		}
		oldContent = string(data)
		newContent = replaceFirst(oldContent, args.OldText, args.NewText)
		return filePath, oldContent, newContent, nil

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			return "", "", "", fmt.Errorf("invalid arguments: %w", err)
		}
		filePath = args.Path
		data, err := os.ReadFile(filePath)
		if err != nil {
			oldContent = ""
		} else {
			oldContent = string(data)
		}
		newContent = args.Content
		return filePath, oldContent, newContent, nil

	default:
		return "", "", "", fmt.Errorf("not a file tool: %s", tc.Name)
	}
}

// replaceFirst replaces the first occurrence of old in s with new.
func replaceFirst(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
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
		isNew := cp.OldContent == ""
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
				IsNew:     cp.OldContent == "",
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
		isNew := cp.OldContent == ""
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
