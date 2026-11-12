package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	runtimedebug "runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/metrics"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

func (a *Agent) permissionDeniedMessage(toolName string) string {
	mode := a.currentMode()
	var b strings.Builder
	fmt.Fprintf(&b, "Permission denied for tool %q. The operation was blocked by the permission policy (current mode: %s).\n", toolName, mode)
	if mode == permission.PlanMode {
		b.WriteString("Plan mode denies explicit file-writing tools until the mode changes (reads, shell probes, and builds are allowed). " +
			"If you did not intend to operate in plan mode, call exit_plan_mode to restore the previous mode (" +
			"supervised or bypass), or present your plan via exit_plan_mode.\n")
	}
	b.WriteString("To proceed:\n" +
		"1. If this is a file write, ensure the path is within the workspace\n" +
		"2. Ask the user to switch to a more permissive mode (Shift+Tab) or approve the specific operation\n" +
		"3. Do NOT retry the same operation - it will be denied again")
	return b.String()
}

func (a *Agent) executeToolWithPermission(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
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
				"denied by permission policy (mode-independent rule)", 0, "")
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
				debug.Log("approval-memory", "auto-approved %s (learned pattern)", tc.Name)

				break
			}
			if onApproval != nil {

				if a.askThrottle.ShouldSuppress(tc.Name, tc.Arguments) {
					a.auditToolResult(tc.Name, tc.Arguments, audit.StatusUserDenied, "suppressed by ask throttle (repeated denial)", 0, "")
					debug.Log("approval-throttle", "suppressed re-ask for %s (denied twice recently)", tc.Name)
					return tool.Result{
						Content: fmt.Sprintf("Permission denied for tool %q without prompting: this exact request was denied twice within the last minute. Re-asking is blocked to avoid nagging the user. Change your approach (different tool, different target, or abandon this step) instead of repeating the request.", tc.Name),
						IsError: true,
					}
				}
				resp := onApproval(ctx, tc.Name, string(tc.Arguments))
				if resp == permission.Deny {

					a.auditToolResult(tc.Name, tc.Arguments, audit.StatusUserDenied, "user denied at approval gate", 0, "")
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

					note := "approval timed out (no user response)"
					if resp == permission.Cancelled {
						note = "approval cancelled (run interrupted or request displaced)"
					}
					a.auditToolResult(tc.Name, tc.Arguments, audit.StatusAskTimeout, note, 0, "")
					debug.Log("approval-gate", "ask ended without user decision for %s (%v)", tc.Name, resp)
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

	toolStart := time.Now()
	result := a.executeToolWithTimeout(ctx, tc)
	toolDur := time.Since(toolStart)

	if toolDur > slowToolThreshold {
		debug.Log("agent", "slow tool: %s took %v", tc.Name, toolDur)
	}

	if latencyHint := a.latencyTracker.RecordAndCheck(tc.Name, toolDur); latencyHint != "" {
		a.appendGuidance(&result, latencyHint)
	}

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

const defaultToolTimeout = 5 * time.Minute

const slowToolThreshold = 30 * time.Second

var toolsWithoutTimeout = map[string]bool{

	"run_command":         true,
	"start_command":       true,
	"wait_command":        true,
	"read_command_output": true,
	"write_command_input": true,
	"stop_command":        true,
	"sleep":               true,

	"wait_agent":       true,
	"use_namedagent":   true,
	"delegate":         true,
	"spawn_agent":      true,
	"task_output":      true,
	"teammate_results": true,

	"ask_user": true,
}

func (a *Agent) executeToolWithTimeout(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if toolsWithoutTimeout[tc.Name] {
		return a.executeTool(ctx, tc)
	}

	timeout := a.latencyTracker.computeAdaptiveTimeout(tc.Name)

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
		cancel()
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

func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCallDelta) tool.Result {

	tc = a.inheritDelegationConstraints(tc)

	runTraced := func() tool.Result {
		res := a.executeToolInner(ctx, tc)
		a.wfTraceRecord(tc, res)
		return res
	}
	if suppressed := a.dedupLedger().suppressDuplicate(tc.Name, string(tc.Arguments)); suppressed != nil {
		return *suppressed
	}

	if wf := a.workflowEngineLazy(); wf != nil {
		if wv := wf.checkPreconditions(tc.Name, tc.Arguments); wv != nil {
			var msg string
			if wv.WantGlob == "" {
				msg = fmt.Sprintf("[workflow:%s %s] %q is guarded by step %q which requires step %q first, and the guarded command for that prerequisite was not executed this run. Complete the prerequisite (or fix the workflow spec) before running this command.",
					wv.Step.ID, strings.ToUpper(wv.Step.Mode), wv.Command, wv.Step.ID, wv.Missing)
			} else {
				msg = fmt.Sprintf("[workflow:%s %s] %q is guarded by step %q which requires step %q first, and no artifact matching %q was produced this run. Complete the prerequisite (or fix the workflow spec) before running this command.",
					wv.Step.ID, strings.ToUpper(wv.Step.Mode), wv.Command, wv.Step.ID, wv.Missing, wv.WantGlob)
			}
			msg += wfAttemptAttribution(wv)
			if wv.Step.Message != "" {
				msg += " Note: " + wv.Step.Message
			}
			if wv.Step.Mode == "block" {
				a.auditToolResult(tc.Name, tc.Arguments, audit.StatusInvalid, msg, 0, "workflow:"+wv.Step.ID)
				debug.Log("agent", "[workflow-spec] BLOCK %s before %s (missing %s)", wv.Command, wv.Step.ID, wv.Missing)
				return tool.Result{Content: msg, IsError: true}
			}
			res := runTraced()
			a.dedupLedger().record(tc.Name, string(tc.Arguments), res)

			if !res.IsError {
				a.appendCrashSidecar(tc.Name, string(tc.Arguments))
			}

			a.recordWriteProducts(tc, res)

			a.groundWorkflowAfterTool(tc, res)
			res.Content += "\n\n" + msg
			return res
		}
	}
	if e := a.invariantEngineLazy(); e != nil {
		if v := e.check(tc.Name, tc.Arguments); v != nil {
			if v.Inv.Mode == "block" {
				msg := fmt.Sprintf("[invariant:%s BLOCKED] %s (target: %q op: %s). The call was rejected by a declared behavior invariant; adjust the target or the invariant file.",
					v.Inv.ID, v.Inv.Message, v.Target, v.Op)
				a.auditToolResult(tc.Name, tc.Arguments, audit.StatusInvalid, msg, 0, v.Inv.ID)
				debug.Log("agent", "[invariants] BLOCK %s on %s target=%q", v.Inv.ID, tc.Name, v.Target)
				return tool.Result{Content: msg, IsError: true}
			}
			res := runTraced()
			a.dedupLedger().record(tc.Name, string(tc.Arguments), res)

			if !res.IsError {
				a.appendCrashSidecar(tc.Name, string(tc.Arguments))
			}

			a.recordWriteProducts(tc, res)
			res.Content += fmt.Sprintf("\n\n[invariant:%s] %s (target: %q) - warn-mode invariant matched; proceed carefully.", v.Inv.ID, v.Inv.Message, v.Target)
			auditInvariantWarn(tc.Name, v.Inv.ID)

			a.groundWorkflowAfterTool(tc, res)
			return res
		}
	}
	res := runTraced()
	a.dedupLedger().record(tc.Name, string(tc.Arguments), res)

	if !res.IsError {
		a.appendCrashSidecar(tc.Name, string(tc.Arguments))
	}

	a.recordWriteProducts(tc, res)

	a.groundWorkflowAfterTool(tc, res)
	return res
}

func (a *Agent) recordWriteProducts(tc provider.ToolCallDelta, res tool.Result) {
	if res.IsError {
		return
	}
	e := a.invariantEngineLazy()
	if e == nil {
		return
	}
	for _, ot := range invariantOpTargets(tc.Name, tc.Arguments) {
		if ot.Op == "write" || ot.Op == "mkdir" || ot.Op == "move" {
			e.recordProduct(ot.Target)
		}
	}
}

func (a *Agent) groundWorkflowAfterTool(tc provider.ToolCallDelta, res tool.Result) {
	wf := a.workflowEngineLazy()
	if wf == nil || res.IsError {
		return
	}
	for _, ot := range invariantOpTargets(tc.Name, tc.Arguments) {
		if ot.Op == "write" || ot.Op == "mkdir" || ot.Op == "move" {
			wf.recordCompletion(ot.Target)
		}
	}
	if isCommandExecTool(tc.Name) {
		wf.probeArtifactsOnDisk()
	}
}

func (a *Agent) invariantEngineLazy() *invariantEngine {
	a.mu.RLock()
	e := a.invEngine
	wd := a.workingDir
	a.mu.RUnlock()
	if e != nil {
		return e
	}
	if wd == "" {
		return nil
	}
	newE := &invariantEngine{loadDir: filepath.Join(wd, ".ggcode")}
	a.mu.Lock()
	if a.invEngine == nil {
		a.invEngine = newE
	}
	a.mu.Unlock()
	return a.invEngine
}

func auditInvariantWarn(toolName, id string) {
	debug.Log("agent", "[invariants] WARN %s on %s", id, toolName)
}

func (a *Agent) executeToolInner(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}

	if a.onToolProgress != nil && ctx.Value(tool.ToolProgressKey{}) == nil {
		fn := a.onToolProgress
		toolID := tc.ID
		ctx = context.WithValue(ctx, tool.ToolProgressKey{}, tool.ToolProgressFunc(
			func(_, toolName, output string) {
				fn(toolID, toolName, output)
			},
		))
	}

	if tc.Name == ToolSearchToolName && a.toolSearch != nil {
		return a.toolSearch.executeResult(tc.Arguments)
	}

	if tc.Name == memoryToolName && a.memoryTool != nil {
		return a.memoryTool.executeResult(tc.Arguments, a.workingDir)
	}

	t, ok := a.tools.Get(tc.Name)
	if !ok {
		return tool.Result{Content: tool.FormatUnknownToolError(a.tools, tc.Name), IsError: true}
	}

	if a.toolSearch != nil && a.toolSearch.maybeAutoActivate(tc.Name) {
		debug.Log("agent", "tool search: auto-activated schema for %s called by name", tc.Name)
	}

	if repaired, ok := provider.RepairJSON(tc.Arguments); ok {
		debug.Log("agent", "repaired malformed JSON arguments for tool %s", tc.Name)
		tc.Arguments = repaired
	} else if trimmed := bytes.TrimSpace(tc.Arguments); len(trimmed) > 0 && !json.Valid(trimmed) {

		detail := describeJSONError(trimmed)
		debug.Log("agent", "arguments for tool %s are unrecoverable JSON: %s", tc.Name, detail)
		return tool.Result{
			Content: fmt.Sprintf(
				"Tool %q: arguments are not valid JSON and could not be auto-repaired (%s). "+
					"Resend the complete arguments as valid JSON: double-quoted keys and string values, no trailing commas, no single quotes.",
				tc.Name, detail),
			IsError: true,
		}
	}

	coercedArgs := tool.CoerceArguments(t.Parameters(), tc.Arguments)
	if !bytes.Equal(coercedArgs, tc.Arguments) {
		debug.Log("agent", "coerced arguments for tool %s", tc.Name)
		tc.Arguments = coercedArgs
	}

	enumCorrected := tool.CoerceEnumValues(t.Parameters(), tc.Arguments)
	if !bytes.Equal(enumCorrected, tc.Arguments) {
		debug.Log("agent", "enum-corrected arguments for tool %s", tc.Name)
		tc.Arguments = enumCorrected
	}

	if withFallback := tool.InjectDescriptionFallback(t.Parameters(), tc.Arguments, tc.Name); !bytes.Equal(withFallback, tc.Arguments) {
		debug.Log("agent", "injected fallback description label for tool %s", tc.Name)
		tc.Arguments = withFallback
	}

	if missingMsg := tool.ValidateRequiredParams(t.Parameters(), tc.Arguments, tc.Name); missingMsg != "" {
		debug.Log("agent", "required param validation failed for %s: %s", tc.Name, missingMsg)
		return tool.Result{
			Content: fmt.Sprintf("Tool %q: %s. Please provide all required parameters and retry.", tc.Name, missingMsg),
			IsError: true,
		}
	}

	if constraintMsg := tool.ValidateSchemaConstraints(t.Parameters(), tc.Arguments); constraintMsg != "" {
		debug.Log("agent", "schema constraint validation failed for %s: %s", tc.Name, constraintMsg)
		return tool.Result{
			Content: fmt.Sprintf("Tool %q: %s.", tc.Name, constraintMsg),
			IsError: true,
		}
	}

	if blockMsg := a.refusalLedger.checkBlocked(tc.Name, string(tc.Arguments)); blockMsg != "" {
		debug.Log("agent", "refusal ledger blocked %s", tc.Name)
		return tool.Result{
			Content: blockMsg,
			IsError: true,
		}
	}

	destructiveWarning := a.checkGitDestructive(tc.Name, tc.Arguments)

	strippedArgs := tool.StripUnknownParams(t.Parameters(), tc.Arguments)
	if !bytes.Equal(strippedArgs, tc.Arguments) {
		debug.Log("agent", "stripped unknown params for tool %s", tc.Name)
		tc.Arguments = strippedArgs
	}

	a.mu.RLock()
	hookCfg := a.hookConfig
	workDir := a.workingDir
	a.mu.RUnlock()

	// #5212: raw git branch -D bypasses every tool-seam guard, so probe
	// BEFORE execution and hard-block while unpushed commits exist (the
	// post-exec advisory below cannot save a branch that is already gone).
	if tc.Name == "run_command" || tc.Name == "start_command" {
		var m map[string]any
		if err := json.Unmarshal(tc.Arguments, &m); err == nil {
			if cmd, ok := m["command"].(string); ok && cmd != "" {
				if blockMsg := branchDeleteGuardBlock(workDir, cmd); blockMsg != "" {
					debug.Log("agent", "branch-delete guard blocked pre-exec")
					return tool.Result{Content: blockMsg, IsError: true}
				}
			}
		}
	}
	env := hooks.HookEnv{
		ToolName:   tc.Name,
		WorkingDir: workDir,
		FilePath:   hooks.ExtractFilePath(tc.Name, string(tc.Arguments)),
		RawInput:   string(tc.Arguments),
	}

	preResult := hooks.RunPreHooks(hookCfg.PreToolUse, env)
	if !preResult.Allowed {
		return tool.Result{Content: preResult.Output, IsError: true}
	}

	if tc.Name == "multi_file_edit" || tc.Name == "multi_edit_file" ||
		tc.Name == "multi_file_write" || tc.Name == "batch_replace" {
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
		return a.executeUndoEdit(ctx, tc, env)
	}

	syncToolWorkingDir(t, workDir)

	a.mu.Lock()
	a.lastTool = t.Name()
	a.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}
	}
	var shellPre *shellSnapshot
	if tc.Name == "run_command" {
		a.mu.RLock()
		shellCpMgr := a.checkpoints
		a.mu.RUnlock()
		if shellCpMgr != nil {
			shellPre = a.takeShellSnapshot()
		}
	}
	toolStart := time.Now()

	prevMutAmbiguous := a.mutateLedger.lookupAmbiguous(t.Name(), tc.Arguments)
	result := a.executeWithTransientRetry(ctx, t.Name(), tc.Arguments, func(execCtx context.Context, args []byte) (tool.Result, error) {
		return a.safeExecute(t, execCtx, args)
	})
	toolDur := time.Since(toolStart)

	result = a.annotateMutatingOutcome(t.Name(), tc.Arguments, result, prevMutAmbiguous)

	if shellPre != nil && !result.IsError {
		a.mu.RLock()
		shellCpMgr := a.checkpoints
		a.mu.RUnlock()
		if shellCpMgr != nil {
			shellPre.checkpointMutations(takeShellSnapshotIn(shellPre.root), tc.Name, shellCpMgr)
		}
	}

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

	if result.IsError || toolFallbackHintOnSuccess(t.Name()) {
		if hint := toolFallbackHint(t.Name(), result.Content); hint != "" {

			a.appendGuidance(&result, strings.TrimLeft(hint, "\n"))
		}
	}

	if destructiveWarning != "" {
		a.appendGuidance(&result, destructiveWarning)
	}

	if !result.IsError {
		result.Content = guardPromptInjection(tc.Name, tc.Arguments, result.Content)
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

	for i := range plans {
		cur, rerr := os.ReadFile(plans[i].Path)
		if rerr != nil {
			continue
		}
		if string(cur) != plans[i].OldContent {
			debug.Log("agent", "#1786 baseline drift on %s: refreshing plan baseline", plans[i].Path)
			plans[i].OldContent = string(cur)
		}

		if !plans[i].Existed {
			plans[i].Existed = true
		}
	}

	refreshedBatch := make([]fileEditPlan, 0, len(plans))
	for _, p := range plans {
		if diff.HasChanges(p.OldContent, p.NewContent) {
			refreshedBatch = append(refreshedBatch, fileEditPlan{
				Path:       p.Path,
				OldContent: p.OldContent,
				NewContent: p.NewContent,
			})
		}
	}
	if blockers := dryRunValidateBatch(refreshedBatch); len(blockers) > 0 {
		var b strings.Builder
		b.WriteString("[Multi-file edit blocked by post-refresh validation]\n")
		b.WriteString("File content drifted during the confirmation pause and now has fatal issues. NO files were modified.\n\n")
		for path, msg := range blockers {
			b.WriteString(fmt.Sprintf("File: %s\n%s\n\n", path, msg))
		}
		return tool.Result{Content: strings.TrimRight(b.String(), "\n"), IsError: true}
	}

	multiStart := time.Now()
	result, err := a.safeExecute(t, ctx, tc.Arguments)
	multiDur := time.Since(multiStart)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v%s", err, cancellationFlattenNote(err)), IsError: true}
	}

	if cpMgr != nil && len(plans) > 0 {
		a.stampEditIntent()
		var outcome tool.MultiFileEditContent
		if err := json.Unmarshal([]byte(result.Content), &outcome); err == nil {
			planByPath := make(map[string]tool.PlannedFileEdit, len(plans))
			for _, plan := range plans {
				planByPath[plan.Path] = plan
			}
			for _, path := range outcome.WrittenPaths {
				if plan, ok := planByPath[path]; ok {

					cpMgr.SaveWithExistence(path, plan.OldContent, plan.NewContent, tc.Name, plan.Existed)
				}
			}
		}
	}

	var dryProbe struct {
		DryRun bool `json:"dry_run"`
	}
	isDryRun := json.Unmarshal([]byte(result.Content), &dryProbe) == nil && dryProbe.DryRun
	var wpProbe struct {
		WrittenPaths *[]string `json:"written_paths"`
	}
	writtenSet := map[string]bool(nil)
	if json.Unmarshal([]byte(result.Content), &wpProbe) == nil && wpProbe.WrittenPaths != nil {
		writtenSet = make(map[string]bool, len(*wpProbe.WrittenPaths))
		for _, p := range *wpProbe.WrittenPaths {
			writtenSet[p] = true
		}
	}
	if !result.IsError && len(plans) > 0 && !isDryRun {
		a.runPostWriteWarnings(&result, plans, func(path, oldContent, newContent string) string {
			if writtenSet != nil && !writtenSet[path] {
				return ""
			}

			mirrored := mirrorWriteTimeGoFormat(path, newContent)
			return checkWriteIntegrity(path, oldContent, mirrored)
		}, writtenSet)
	}

	if !result.IsError && !isDryRun {
		a.runPostWriteWarnings(&result, plans, CheckMissingTestCompanionWithFS, writtenSet)
	}

	if !result.IsError && !isDryRun {
		a.runPostWriteWarnings(&result, plans, checkDebugStmts, writtenSet)
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

func (a *Agent) runPostWriteWarnings(result *tool.Result, plans []tool.PlannedFileEdit, checker func(path, oldContent, newContent string) string, writtenSet map[string]bool) {
	if len(plans) == 0 {
		return
	}
	var warnings []string
	for _, plan := range plans {
		if writtenSet != nil && !writtenSet[plan.Path] {
			continue
		}
		if diff.HasChanges(plan.OldContent, plan.NewContent) {
			if w := checker(plan.Path, plan.OldContent, plan.NewContent); w != "" {
				warnings = append(warnings, w)
			}
		}
	}
	for _, w := range warnings {
		a.appendGuidance(result, w)
	}
}

func (a *Agent) safeExecute(t tool.Tool, ctx context.Context, args json.RawMessage) (result tool.Result, err error) {
	startTime := time.Now()

	if r := preflightRequiredCheck(t, args); r != nil {

		a.auditToolResult(t.Name(), args, audit.StatusInvalid, r.Content, time.Since(startTime))
		return *r, nil
	}

	if r := preflightArgComplexityCheck(a.argComplexityGate, t, args); r != nil {
		a.auditToolResult(t.Name(), args, audit.StatusInvalid, r.Content, time.Since(startTime))
		return *r, nil
	}

	// #decision-audit: persist the rationale the model attached (it is
	// about to be stripped from args and would otherwise be lost).
	a.pendingAuditRationale = extractArgRationale(args)
	args = stripArgRationale(args)

	if res, err, handled := a.replayToolCall(t.Name(), args); handled {
		return res, err
	}

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
					Content: fmt.Sprintf("tool %s panicked: %v - this is a bug, please report it", t.Name(), r),
					IsError: true,
				}, nil}
			}
		}()
		r, e := t.Execute(ctx, args)
		ch <- execResult{r, e}
	})

	select {
	case r := <-ch:
		a.recordToolCall(ctx, t.Name(), args, r.result, r.err)
		a.auditToolExecution(t.Name(), args, r.result, r.err, time.Since(startTime))
		return r.result, r.err
	case <-ctx.Done():
		debug.Log("agent", "tool %s cancelled via context (Execute did not honor cancellation, goroutine leaked)", t.Name())

		a.auditToolResult(t.Name(), args, audit.StatusCancelled,
			"cancelled via context; tool may still be finishing in the background", time.Since(startTime))
		return tool.Result{
			Content: fmt.Sprintf("tool %s was cancelled (it did not respond to cancellation and may still be finishing in the background)", t.Name()),
			IsError: true,
		}, nil
	}
}

func (a *Agent) executeFileTool(ctx context.Context, t tool.Tool, tc provider.ToolCallDelta, env hooks.HookEnv) tool.Result {
	a.mu.Lock()
	cpMgr := a.checkpoints
	diffFn := a.diffConfirm
	a.mu.Unlock()

	filePath, oldContent, newContent, fileExisted, err := a.computeFileChange(tc)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("file change error: %v", err), IsError: true}
	}

	if diff.HasChanges(oldContent, newContent) {
		if blockMsg := dryRunValidate(filePath, oldContent, newContent); blockMsg != "" {
			return tool.Result{Content: blockMsg, IsError: true}
		}
	}

	if diffFn != nil && diff.HasChanges(oldContent, newContent) {
		diffText := diff.Stats(oldContent, newContent) + "\n" + diff.UnifiedDiff(oldContent, newContent, 3)
		if !diffFn(ctx, filePath, diffText) {
			return tool.Result{Content: fmt.Sprintf("File write to %s cancelled by user.", filePath), IsError: true}
		}
	}

	a.mu.RLock()
	hookCfg2 := a.hookConfig
	a.mu.RUnlock()

	if fileExisted {
		if cur, rerr := os.ReadFile(filePath); rerr == nil && string(cur) != oldContent {
			debug.Log("agent", "#1786 baseline drift on %s: refreshing checkpoint baseline (%d -> %d bytes)",
				filePath, len(oldContent), len(cur))
			oldContent = string(cur)

			if diff.HasChanges(oldContent, newContent) {
				if blockMsg := dryRunValidate(filePath, oldContent, newContent); blockMsg != "" {
					return tool.Result{Content: blockMsg, IsError: true}
				}
			}
		}
	} else if cur, rerr := os.ReadFile(filePath); rerr == nil {

		fileExisted = true
		oldContent = string(cur)

		if diff.HasChanges(oldContent, newContent) {
			if blockMsg := dryRunValidate(filePath, oldContent, newContent); blockMsg != "" {
				return tool.Result{Content: blockMsg, IsError: true}
			}
		}
	}

	fileStart := time.Now()
	result, err := a.safeExecute(t, ctx, tc.Arguments)
	fileDur := time.Since(fileStart)
	if err != nil {
		return tool.Result{Content: fmt.Sprintf("tool error: %v%s", err, cancellationFlattenNote(err)), IsError: true}
	}

	if cpMgr != nil && !result.IsError {
		a.stampEditIntent()
		cpMgr.SaveWithExistence(filePath, oldContent, newContent, tc.Name, fileExisted)
	}

	if !result.IsError {

		newContent = mirrorWriteTimeGoFormat(filePath, newContent)
		if warning := checkWriteIntegrity(filePath, oldContent, newContent); warning != "" {
			a.appendGuidance(&result, warning)
		}

	}

	if !result.IsError {
		if warning := CheckMissingTestCompanionWithFS(filePath, oldContent, newContent); warning != "" {
			a.appendGuidance(&result, warning)
		}
	}

	if !result.IsError {
		if debugWarning := checkDebugStmts(filePath, oldContent, newContent); debugWarning != "" {
			a.appendGuidance(&result, debugWarning)
		}
	}

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

func (a *Agent) computeFileChange(tc provider.ToolCallDelta) (filePath, oldContent, newContent string, fileExisted bool, err error) {
	fileExisted = true
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

			return "", "", "", false, fmt.Errorf("cannot read file: %w", err)
		}
		oldContent = string(data)

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

func replaceFirst(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
}

func simulateEditFile(content, oldText, newText string, replaceAll bool) (string, error) {
	if oldText == "" {
		return "", fmt.Errorf("Error: old_text is required")
	}
	mr := simResolveOldText(content, oldText)
	if mr.canonical == "" {
		msg := "old_text not found in file"
		if hint := tool.NeighborHint(content, oldText); hint != "" {
			msg += ". " + hint
		}
		return "", fmt.Errorf("%s", msg)
	}
	count := strings.Count(content, mr.canonical)
	if !replaceAll && count > 1 && !mr.anchored {
		msg := fmt.Sprintf("old_text found %d times in file - must be unique. Add 1-3 lines of surrounding context to disambiguate, copy the exact numbered lines from read_file to anchor the intended occurrence, or set replace_all=true to replace every occurrence", count)
		if hint := tool.MatchLineHint(count, tool.MatchLines(content, mr.canonical)); hint != "" {
			msg += " " + hint
		}
		return "", fmt.Errorf("%s", msg)
	}

	if !replaceAll && !mr.anchored && count <= 1 {
		if loose, looseLines := tool.LenientRecount(content, oldText, mr.transform); loose > 1 {
			msg := fmt.Sprintf("old_text matched %d times in file under whitespace-tolerant matching - must be unique. Add context to disambiguate or set replace_all=true", loose)
			if len(looseLines) > 0 {
				msg += fmt.Sprintf(" Loose matches start at line(s): %s.", tool.FormatMatchLines(looseLines))
			}
			return "", fmt.Errorf("%s", msg)
		}
	}
	if mr.transform != "" {
		newText = simAdjustNewText(content, newText, mr)
	} else if strings.Contains(oldText, "\r\n") && !strings.Contains(newText, "\r\n") {

		newText = strings.ReplaceAll(newText, "\n", "\r\n")
	}
	if replaceAll {
		return strings.ReplaceAll(content, mr.canonical, newText), nil
	}
	if mr.anchored {
		return content[:mr.start] + newText + content[mr.start+len(mr.canonical):], nil
	}
	return replaceFirst(content, mr.canonical, newText), nil
}

type simMatchResult struct {
	canonical string
	transform string
	shift     string
	trim      string
	start     int
	anchored  bool
}

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

func simAdjustNewText(content, newText string, mr simMatchResult) string {
	out := newText
	if strings.Contains(mr.transform, "read-file-wrapper-stripped") {
		if trimmed, changed := simTrimReadFileWrapperLines(out); changed {
			out = trimmed
		}
	}
	if strings.Contains(mr.transform, "line-numbers-stripped") {

		out = simStripPaddedLineNumberPrefixes(out, content)
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

var simPaddedLineNumberPrefixRE = regexp.MustCompile(`^\s{1,12}\d+\t`)
var simBareLineNumberPrefixRE = regexp.MustCompile(`^(\d+)\t`)

func simStripPaddedLineNumberPrefixes(text, content string) string {
	maxLine := strings.Count(content, "\n") + 1
	lines := simTrimDanglingReadFileLineNumberOnlyLines(strings.Split(text, "\n"))
	for i, l := range lines {
		if simReadFileLineNumberOnlyRE.MatchString(l) {
			lines[i] = ""
			continue
		}
		if simPaddedLineNumberPrefixRE.MatchString(l) {
			lines[i] = simPaddedLineNumberPrefixRE.ReplaceAllString(l, "")
			continue
		}
		if m := simBareLineNumberPrefixRE.FindStringSubmatch(l); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= maxLine {
				lines[i] = simBareLineNumberPrefixRE.ReplaceAllString(l, "")
			}
		}
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

func simLeadingWhitespace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

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

func (a *Agent) executeUndoEdit(ctx context.Context, tc provider.ToolCallDelta, env hooks.HookEnv) tool.Result {
	result := a.executeUndoEditInner(ctx, tc)

	a.mu.RLock()
	hookCfg := a.hookConfig
	a.mu.RUnlock()
	postEnv := env
	postEnv.ToolSuccess = !result.IsError
	if result.IsError {
		postEnv.ToolError = truncateString(result.Content, 500)
	}
	postEnv.ToolResult = truncateString(result.Content, 4096)
	postResult := hooks.RunPostHooks(hookCfg.PostToolUse, postEnv)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}
	return result
}

func (a *Agent) executeUndoEditInner(ctx context.Context, tc provider.ToolCallDelta) tool.Result {
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
		cp, err := cpMgr.Undo("agent")
		if err != nil {

			if errors.Is(err, checkpoint.ErrNothingToUndo) {
				return tool.Result{
					IsError: true,
					Content: fmt.Sprintf("Nothing to undo: %v", err),
				}
			}
			return tool.Result{
				IsError: true,
				Content: fmt.Sprintf("Undo failed (checkpoint store error, not an empty stack): %v", err),
			}
		}

		isNew := !cp.Existed

		a.expiredRead.recordUndo(cp.FilePath)
		result := tool.FormatUndoResult(cp.FilePath, cp.ToolCall, isNew)

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
		cp, revertedFiles, err := cpMgr.RevertWithFilesSource(args.CheckpointID, "agent")
		if err != nil {
			return tool.Result{
				IsError: true,
				Content: fmt.Sprintf("Revert failed: %v", err),
			}
		}
		isNew := !cp.Existed

		for _, f := range revertedFiles {
			a.expiredRead.recordUndo(f)
		}
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

var toolWorkingDirMu sync.Mutex

func syncToolWorkingDir(t tool.Tool, dir string) {
	toolWorkingDirMu.Lock()
	defer toolWorkingDirMu.Unlock()

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

func describeJSONError(raw []byte) string {
	var v any
	err := json.Unmarshal(raw, &v)
	if err == nil {
		return "unknown parse error"
	}
	if se, ok := err.(*json.SyntaxError); ok {
		return fmt.Sprintf("%s at byte offset %d", se.Error(), se.Offset)
	}
	return err.Error()
}
