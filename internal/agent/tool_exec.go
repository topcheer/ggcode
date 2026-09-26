package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/util"
)

// toolBatchOutcome carries one iteration's tool-execution results back to
// RunStreamWithContent. cancelErr short-circuits the caller when the
// context is cancelled mid-batch; the five data fields feed the shared
// post-execution block (result submission, follow-up injection, deferred
// project memory).
type toolBatchOutcome struct {
	toolResults           []provider.ContentBlock
	followUpMessages      []provider.Message
	deferredMemoryContent string
	deferredMemoryFiles   []string
	deferredMemoryTarget  string
	cancelErr             error
}

// executeToolBatch runs the pre-execution detector battery, executes every
// tool call of the current iteration (read-only pre-execution, dedup,
// permission, result post-processing, guidance coalescing) and returns the
// assembled outcomes. Extracted verbatim from RunStreamWithContent; the
// caller resyncs msgs from contextManager because msgs is write-only here.
func (a *Agent) executeToolBatch(ctx context.Context, onEvent func(provider.StreamEvent), toolCalls []provider.ToolCallDelta, iteration int, runStats *RunStats) toolBatchOutcome {
	// Execute tool calls and build tool_result message
	// Reset the no-progress counter — the agent is making forward progress.
	a.strategistNoProgressCount = 0
	var toolResults []provider.ContentBlock
	// Collect follow-up messages from tools (e.g., inline skills)
	var followUpMessages []provider.Message
	// Defer project memory injection until after all tools execute,
	// so every tool_call gets a matching tool_result.
	var deferredMemoryContent string
	var deferredMemoryFiles []string
	var deferredMemoryTarget string
	// Parallel pre-execution of read-only tools (LLMCompiler/W&D-inspired).
	// When the LLM returns multiple tool calls, independent read-only tools
	// are executed concurrently before the sequential loop. Results are
	// consumed in-order; side-effect tools still run sequentially.
	preExecuted := a.preExecuteReadOnlyTools(ctx, toolCalls)
	// Parallel pre-execution of wait-family tools (wait_agent,
	// teammate_results): concurrent waits make batch latency the MAX
	// instead of the SUM of remaining sub-agent runtimes, and every wait's
	// progress streams live (per-toolID callbacks) instead of only the
	// first being visible. Consumed in-order below.
	waitPreExecuted := a.preExecuteWaitTools(ctx, toolCalls)
	// Batch edit conflict detection: when the LLM emits multiple file-editing
	// calls targeting the same file in one batch, warn upfront so the model
	// knows subsequent edits may fail (file content changes after each edit).
	batchConflictWarnings := detectBatchEditConflicts(toolCalls)
	// Deduplicate identical read-only tool calls within the same LLM response.
	// LLMs occasionally emit duplicate calls (e.g., two read_file for the
	// same path). Skip the second execution and reuse the first result.
	type dedupKey struct {
		tool string
		args string
	}
	seenReadOnly := make(map[dedupKey]int) // key → index of first result in toolResults
	// Counterfactual dependency detection: check if this batch of tool calls
	// contains producer-consumer pairs where the consumer assumes the producer
	// has completed (e.g., write_file + run_command build in parallel).
	if depWarn := a.recordToolCallBatch(toolCalls, iteration+1); depWarn != "" {
		debug.Log("agent", "Iteration %d: counterfactual dependency assumption detected", iteration+1)
		a.contextManager.Add(provider.Message{
			Role:    "user",
			Content: []provider.ContentBlock{{Type: "text", Text: depWarn}},
		})
	}
	// Batch coupling detection: check if this batch of tool calls contains
	// hidden sequential dependencies (e.g., mkdir + write_file to that dir).
	if len(toolCalls) > 1 {
		var batchInfos []couplingToolCall
		for _, tc := range toolCalls {
			batchInfos = append(batchInfos, couplingToolCall{name: tc.Name, args: tc.Arguments})
		}
		if couplingWarn := a.batchCoupling.checkBatchCoupling(batchInfos); couplingWarn != "" {
			debug.Log("agent", "Iteration %d: batch tool call coupling detected", iteration+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: couplingWarn}},
			})
		}
	}
	// #1798 case 1: the irreversibility gate is a PRE-action check - it
	// used to sit in the post-execution result loop, so the "You are
	// about to execute" warning arrived after the action had already
	// happened (calibrated abstention had nothing to abstain from).
	// Recording here also keeps the ledger entry alive for the
	// post-execution recordOutcome revoke (#1776).
	if a.irrevGate != nil {
		for _, tc := range toolCalls {
			if warn := a.irrevGate.recordAction(tc.Name, string(tc.Arguments)); warn != "" {
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: warn}},
				})
			}
		}
	}
	for idx, tc := range toolCalls {
		if err := ctx.Err(); err != nil {
			// Context cancelled mid-tool-execution. The assistant message
			// (with tool_use blocks) was already added to contextManager above.
			// Without matching tool_results, the next LLM API call will fail
			// because tool_use has no corresponding tool_result (protocol violation).
			// Fill in "cancelled" results for all tool_calls that have not run yet.
			a.fillCancelledToolResults(toolCalls[idx:], &toolResults)
			return toolBatchOutcome{cancelErr: err}
		}
		// #1799 case 1: undo-blind detection BEFORE execution. The old
		// call site sat in the post-execution result loop: the blind edit
		// had ALREADY landed on disk by the time the "read before editing"
		// guidance arrived - exactly the compounding error this detector
		// exists to prevent. The check is pure (state mutation records the
		// undo/read/mutation sequence), so calling it pre-execution yields
		// identical classification one step earlier; the hint rides the
		// tool result so the LLM sees it in the same turn.
		var undoBlindHint string
		if ub := a.undoBlind.recordToolCall(tc.Name, tc.Arguments); ub != "" {
			debug.Log("agent", "Iteration %d: undo-blind mutation detected (pre-execution)", iteration+1)
			undoBlindHint = ub
		}
		// #1587-A: snapshot write-target existence BEFORE execution -
		// the orphan detector consumes it post-execution.
		a.orphanFile.recordPreExec(tc.Name, string(tc.Arguments), a.workingDir)
		// Track tool call for reflection stats
		runStats.recordToolCall(tc.Name)
		a.toolCallBudget.record()
		a.toolThermal.recordToolCall(tc.Name)
		a.iterPressure.recordToolCall(tc.Name, iteration+1)
		extractPathsFromToolCall(tc.Name, tc.Arguments, runStats)
		// Check for consecutive duplicate tool calls (loop detection).
		// If detected, inject a guidance message into the tool result.
		var loopGuidance string
		if guidance := a.loopDetectionInjection(tc); guidance != "" {
			loopGuidance = guidance
		}
		// Search parameter quality guard: detect overly broad/vague search
		// parameters BEFORE execution to prevent context flooding.
		var searchParamHint string
		if hint := a.searchParamGuard.checkParamQuality(tc.Name, tc.Arguments); hint != "" {
			searchParamHint = hint
		}
		// Pre-action reversibility assessment: evaluate high-stakes actions
		// BEFORE execution. Counterfactual Pre-Mortem Loops (Curve Labs 2026).
		if revGuidance := a.reversibility.checkPreAction(tc.Name, string(tc.Arguments)); revGuidance != "" {
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: revGuidance,
				}},
			})
		}
		// Record safety signals for subsequent reversibility checks.
		a.reversibility.recordSafetySignal(tc.Name, string(tc.Arguments))
		// Tool call redundancy analyzer: detect scattered (non-consecutive)
		// duplicate calls to the same tool with identical arguments.
		var redundancyHint string
		if hint := a.toolRedundancy.recordCall(tc.Name, tc.Arguments); hint != "" {
			redundancyHint = hint
		}
		// Semantic-equivalent duplicate detection: catches calls with
		// reordered JSON keys or volatile metadata fields (trace_id,
		// timestamp) that evade the exact-match redundancy detector.
		var equivHint string
		rawFp := fingerprintToolCall(tc.Name, tc.Arguments)
		if hint := a.toolEquivDetect.recordCall(tc.Name, tc.Arguments, rawFp); hint != "" {
			equivHint = hint
		}
		// Tool overuse / self-awareness detection: warn when the agent
		// calls tools to retrieve information it already has (read-after-
		// write, unchanged dir re-list, trivial env commands).
		// Check for project memory but defer injection
		if mc, mf, mt := a.pendingProjectMemoryForTool(tc); len(mf) > 0 && strings.TrimSpace(mc) != "" {
			if deferredMemoryContent == "" {
				deferredMemoryContent = mc
				deferredMemoryFiles = mf
				deferredMemoryTarget = mt
			}
		}
		// Don't log executeToolWithPermission start — the permission check log already covers this
		// In-turn deduplication: if the LLM sent the same read-only tool call
		// twice in this response, reuse the first result instead of re-executing.
		dedupK := dedupKey{tool: tc.Name, args: string(tc.Arguments)}
		if speculativeSafeTools[tc.Name] {
			if firstIdx, ok := seenReadOnly[dedupK]; ok && firstIdx < len(toolResults) {
				dedupContent := toolResults[firstIdx].Text
				debug.Log("agent", "in-turn dedup: %s already executed in this response, reusing result", tc.Name)
				toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, dedupContent, false))
				onEvent(provider.StreamEvent{
					Type:    provider.StreamEventToolResult,
					Tool:    tc,
					Result:  dedupContent,
					IsError: false,
				})
				continue
			}
		}
		// Check memoization cache: if a read-only tool was called with identical args
		// earlier in this run (and the underlying resource hasn't changed), return the
		// cached result. This prevents redundant re-execution after tool-result clearing.
		// Secret-redaction write guard (#1195): a file-write tool whose
		// arguments contain [REDACTED:*] markers would destroy the real
		// secret stored in the target file. Block before any cache or
		// execution path; the error result carries remediation guidance.
		if redactWarn := checkRedactedInWrite(tc.Name, string(tc.Arguments)); redactWarn != "" {
			toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, redactWarn, true))
			onEvent(provider.StreamEvent{
				Type:    provider.StreamEventToolResult,
				Tool:    tc,
				Result:  redactWarn,
				IsError: true,
			})
			continue
		}
		var result tool.Result
		memoHit := false
		if memoResult, hit := a.toolMemo.get(tc.Name, tc.Arguments); hit && a.speculativeHitAllowed(ctx, tc) {
			result = memoResult
			memoHit = true
			// Annotate cache hits so the model knows this is cached content, not a
			// fresh execution. After tool-result clearing replaces old results with
			// placeholders, the model re-calls the tool and gets identical content back.
			// Without this annotation, the model treats it as new information and
			// re-analyzes identical content (wasting attention budget). The annotation
			// lets the model skip redundant analysis and proceed efficiently.
			// Context-efficient: only added for non-empty, non-error results, and the
			// prefix is capped at 80 chars.
			if result.Content != "" && !result.IsError {
				result.Content = fmt.Sprintf("[cached — %s returned identical content since your last call]\n%s", tc.Name, result.Content)
			}
			debug.Log("memoize", "memo hit for %s (saved tool execution)", tc.Name)
		} else if cachedResult, hit := a.speculator.getCached(tc.Name, tc.Arguments); hit && a.speculativeHitAllowed(ctx, tc) {
			// #1496: the speculative cache used to serve hits with zero
			// permission flow while the sibling preExecuted paths run
			// usePreExecutedWithPermission - a deny(read_file X)+allow(edit
			// X) policy was bypassed. On any non-Allow decision, abandon
			// the hit and fall through to the normal gated execution path
			// (which handles Ask/approval UX properly).
			result = cachedResult
			debug.Log("speculate", "speculative cache hit for %s (saved tool execution)", tc.Name)
		} else if pre, ok := preExecuted[idx]; ok {
			// Parallel pre-execution result (LLMCompiler/W&D-inspired).
			// Runs permission check; if denied, the read-only result is discarded.
			result = a.usePreExecutedWithPermission(ctx, tc, pre)
		} else if pre, ok := waitPreExecuted[idx]; ok {
			// Wait-family parallel result: same permission semantics as
			// read-only pre-execution (waiting is side-effect-free, denial
			// merely discards the snapshot).
			result = a.usePreExecutedWithPermission(ctx, tc, pre)
		} else if cmdCached, hit := a.checkCommandCache(tc.Name, tc.Arguments); hit {
			// Deterministic command cache: skip re-running build/test commands
			// when no source files have changed since the last execution.
			result = cmdCached
		} else {
			result = a.executeToolWithPermission(ctx, tc)
			// Cache deterministic command results (build, test, lint, etc.)
			// for reuse when the same command is called again without file changes.
			a.storeCommandResult(tc.Name, tc.Arguments, result)
			// Effect ledger (LangEffect/RAC-inspired): record failed/uncertain
			// shell executions and, when the identical command is retried after
			// such an attempt, annotate the retry result so the model verifies
			// external state instead of trusting an outcome that may duplicate
			// side effects. Runs after storeCommandResult: only raw results are
			// cached (annotation text must never be cached).
			if hint := a.recordEffectAttempt(tc.Name, tc.Arguments, result); hint != "" {
				result.Content += hint
			}
		}
		// Secret redaction (#1195): mask secret values in external-content
		// tool results BEFORE any recorder, cache annotation, context append,
		// or session-history persistence sees them. Applies uniformly to all
		// result paths above (memo/speculative/pre-executed/command-cache/execute).
		result.Content = redactSecrets(tc.Name, result.Content)
		// Record the tool call for speculative pattern learning.
		a.speculator.recordObservation(tc.Name)
		// Track todo_write usage for the agent-side planner: once the
		// agent creates a todo list, plan suggestions and reminders stop.
		if tc.Name == "todo_write" && !result.IsError {
			a.plannerMarkTodoCreated()
			// Track for stale todo detection: record the iteration so we
			// can detect plan abandonment if the agent stops updating.
			todoCount := parseTodoCount(tc.Arguments)
			a.recordTodoStalenessUpdate(iteration+1, todoCount)
			// Check for silent todo item removal (contract drop).
			if hint := a.checkTodoDrop(tc.Arguments); hint != "" {
				a.appendGuidance(&result, hint)
			}
		}
		// File-editing tools invalidate the speculative cache: any
		// pre-executed read_file/grep results for edited files are now
		// stale. Clear the cache to prevent serving outdated content.
		// #1104: undo_edit is included - checkpoint restore rewrites the
		// file, and without invalidation the next read could be served
		// from the memoize/speculator/command caches describing the
		// pre-undo state. See mutatesSourceTree in verify_hint.go.
		if mutatesSourceTree(tc.Name) && !result.IsError {
			a.speculator.invalidateCache()
			// Git whole-tree operations (checkout, reset, revert) change
			// potentially all files at once. They need nuclear invalidation:
			// clear mtime-based entries too, because cached reads from the
			// old branch are now wrong even if individual file mtimes
			// happened to not change.
			if gitWholeTreeTools[tc.Name] {
				a.toolMemo.invalidateAll()
				debug.Log("agent", "whole-tree git operation %s: invalidated all caches", tc.Name)
			} else {
				// Normal file edit or partial git op: invalidate
				// TTL-based memoize entries (grep, LSP, git) whose
				// results may be stale. mtime-based entries are kept.
				a.toolMemo.invalidateTTLBased()
			}
			// Invalidate the deterministic command cache: any build/test
			// results are now stale because source files changed.
			a.commandCache.invalidate()
			// Record created files so the unread-edit guard exempts them.
			for _, p := range extractCreateFilePaths(tc.Name, tc.Arguments) {
				a.unreadEdit.recordCreated(p)
				a.tunnelVision.recordFile(p)
				a.fileFreshness.recordWrite(p)
				a.readHash.recordWriteHash(p)
			}
			// Track edit for recurring-error detection: increments the
			// "edits since last build error" counter so that a recurring
			// error with edits in between is flagged as a root-cause gap.
			a.recurringErrorRecordEdit()
			// Mark edited files as dirty in the code index so the
			// background indexer can update them incrementally.
			if a.codeIndex != nil {
				a.codeIndex.MarkDirty(extractEditedPaths(tc))
			}
		}
		// #750: shell commands can mutate sources too (gofmt -w, sed -i,
		// git apply, go mod tidy...). They are invisible to the tool-name
		// gate above, so cached build/test results would be served stale
		// with a false "no source files have changed" annotation -- or a
		// stale PASS after a bad patch (false green light). Reuse the
		// #749 shellMutatesSources heuristic; FP cost is one lost cache
		// reuse, FN cost is executing on wrong results.
		// #1028: a failed compound command can still have mutated sources
		// (e.g. `sed -i ... && make lint` -- sed rewrote the file, make failed).
		// The side effects already happened, so the IsError gate must not
		// skip invalidation here; a stale "no source files have changed"
		// cache hit would be actively wrong. FP cost: one lost cache reuse.
		if tc.Name == "run_command" || tc.Name == "start_command" {
			if cmd, _ := parseRunCommandArgs(tc.Arguments); shellMutatesSources(cmd) {
				a.speculator.invalidateCache()
				a.toolMemo.invalidateTTLBased()
				a.commandCache.invalidate()
				// #1486: the shell rewrote sources, so re-running a build/test
				// CAN produce new information - keep the reverify detector in
				// agreement with the caches we just invalidated.
				a.redundantReverify.recordShellSourceMutation()
				debug.Log("agent", "shell source mutation %q (failed cmd included): invalidated command/speculator/memo caches", cmd)
			}
		}
		// Store result in memoization cache for read-only tools.
		// #983: skip the put on a memo hit — the cached entry already holds
		// the pristine result, and re-putting the annotated copy (with the
		// "[cached ...]" prefix and any appended hints) would stack one
		// prefix per repeated call and make the "identical content"
		// annotation literally false from the second layer on.
		if speculativeSafeTools[tc.Name] && !result.IsError && !memoHit {
			a.toolMemo.put(tc.Name, tc.Arguments, result)
		}
		// Track files read during this run so the unread-edit guard
		// knows which files the agent has seen.
		if (tc.Name == "read_file" || tc.Name == "multi_file_read") && !result.IsError {
			// #1476-B: patch-exhaustion counts CALLS, not paths - the
			// IFT give-up rule models successive probes (diminishing
			// returns per RE-visit), and #500's per-path accounting turned
			// ONE multi_file_read over 4 same-package files (the tool's
			// documented coordinated-edit prep workflow) into an instant
			// false 'over-mining' hit. The hint fires once per call on
			// the LAST path only.
			readPathsLen := len(extractReadFilePaths(tc.Name, tc.Arguments))
			// #1782 case 3: a windowed read (read_file with offset/limit)
			// must not mark the file FULLY read. recordRead had no window
			// notion, so `read_file {offset:2000, limit:50}` set
			// filesRead=true and a follow-up edit outside the window
			// passed the unread guard silently (#463 fixed the redundant
			// read side only - the fix that reached exactly one consumer).
			// The other three recorders below are disk-based (they stat or
			// hash the file itself, not the returned slice), so a windowed
			// read is equivalent to a full read for them - unchanged.
			hasWindow := readArgsHaveWindow(tc.Arguments)
			for pi, p := range extractReadFilePaths(tc.Name, tc.Arguments) {
				a.unreadEdit.recordReadWindow(p, hasWindow)
				a.tunnelVision.recordFile(p)
				a.editFailRecovery.recordRead(p)
				a.fileFreshness.recordRead(p)
				a.readHash.recordReadHash(p)
				if hint := a.redundantRead.checkRedundantRead(p, hasWindow); hint != "" {
					a.appendGuidance(&result, hint)
				}
				if pi == readPathsLen-1 {
					if hint := a.patchExhaust.recordRead(p); hint != "" {
						a.appendGuidance(&result, hint)
					}
				}
			}
		}
		// #476: search-tool output counts toward exploration breadth —
		// grep/code_search/files_with_matches results name the files the
		// agent has effectively "looked at". Without this, a 12-file grep
		// sweep plus 2 read_file's scored as 2 files and triggered a
		// bogus "broaden exploration" warning.
		if searchResultTools[tc.Name] && !result.IsError {
			for _, p := range extractSearchResultPaths(result.Content) {
				a.tunnelVision.recordSearched(p)
			}
		}
		// Unread-file edit guard: warn when editing a file not read in
		// this run. Fires before the tool executes so the hint is in the
		// result alongside any error from the edit attempt.
		if !result.IsError && fileEditingTools[tc.Name] {
			// #1454-C: was a hand-rolled 3-tool list; write_file/batch_replace/
			// lsp_rename/file_ops/notebook_edit successes never reset the
			// failure counter (recordEditSuccess below), violating
			// edit_fail_recovery's documented "resets on successful edit".
			// Counted ONCE per tool call: this used to sit inside the per-file
			// loop below, so one multi_file_edit touching 2 files doubled the
			// refactor counter and hit the threshold of 2 instantly (#487).
			a.prematureRefactorRecordEdit(tc.Name, tc.Arguments)
			for _, p := range extractEditFilePaths(tc.Name, tc.Arguments) {
				a.editFailRecovery.recordEditSuccess(p)
				// Content-fingerprint validation MUST run before readHash.recordWriteHash
				// below: recordWriteHash deletes the stored hash, so validating after
				// it always misses and the detector is dead in production (#283).
				// Catches sub-second edits that mtime misses and suppresses false
				// positives from touch/NFS. Mirrors the #168 ordering precedent.
				if hint := a.readHash.validateContentAtEdit(p, extractOldTextLen(tc.Name, tc.Arguments)); hint != "" {
					a.appendGuidance(&result, hint)
				}
				a.fileFreshness.recordWrite(p)
				a.readHash.recordWriteHash(p)
				a.redundantRead.recordWrite(p)
				// Refresh the stale-read baseline: the agent's own edit just
				// bumped the mtime — without this, checkStaleRead would flag
				// our own edit as an external modification (#168).
				a.unreadEdit.recordWrite(p)
				if a.tokenWasteBudget != nil {
					a.tokenWasteBudget.markFileEdited(p)
				}
				a.patchExhaust.recordEdit(p)
				// Convergence lock: track post-verification edits.
				a.convergenceRecordEdit(tc.Name)
				// Diminishing edit: track edit substance size for polish-spiral detection.
				a.diminishingRecordEdit(tc.Name, tc.Arguments)
				// Overcorrection cascade: track edit size vs error severity.
				// #1823 case 3: gated behind claimsSupervision — same class of
				// lexical/byte-count heuristic as the claims family, same noise
				// asymmetry argument. Ungated it enforced only the
				// “shrink your edit” side while the “verify before claiming”
				// side stayed opt-in — a directional bias opposite to the
				// paired-axes design intent.
				if a.claimsSupervision {
					if ocHint := a.overcorrectionRecordEdit(tc.Name, tc.Arguments); ocHint != "" {
						a.appendGuidance(&result, ocHint)
					}
				}
				// Fix cascade: track edits for wrong-hypothesis lock-in detection.
				a.fixCascade.recordEdit()
				// Error regression: track edits for negative progress detection.
				a.errRegression.recordEdit()
				if hint := a.unreadEdit.checkUnreadEdit(p); hint != "" {
					a.appendGuidance(&result, hint)
				}
				// Stale-read detection: warn when the file was modified on
				// disk since the last read (external edit, git pull, etc.).
				if hint := a.unreadEdit.checkStaleRead(p); hint != "" {
					a.appendGuidance(&result, hint)
				}
				// Expired-read detection: warn when the agent edits a file
				// it previously read, marking the prior read as expired.
				if hint := a.expiredRead.recordEdit(p); hint != "" {
					a.appendGuidance(&result, hint)
				}
				// #1459-B: a rolled-back edit restores the pre-edit
				// content - the file's read/edit bookkeeping must forget
				// it so the correct anchor-rebuilding re-read isn't
				// later misreported as stale.
				if tc.Name == "undo_edit" && !result.IsError {
					a.expiredRead.recordUndo(p)
				}
				// Export guard: detect breaking changes to exported Go symbols
				// (removed functions, changed signatures) by comparing against
				// git HEAD. Fires once per file per run.
				if hint := a.checkExportGuard(p); hint != "" {
					a.appendGuidance(&result, hint)
				}
				// Hub package guard: per-edit blast-radius awareness for
				// widely-imported packages. Complements export_guard by
				// providing scale context even for non-breaking edits.
				if hint := a.checkHubPackage(p); hint != "" {
					a.appendGuidance(&result, hint)
				}
				// Generated artifact guard: warn when editing lock files,
				// generated code, vendored files, or files with DO NOT EDIT
				// headers. Suggests the correct regeneration command.
				if hint := a.artifactGuard.checkGeneratedArtifact(p); hint != "" {
					a.appendGuidance(&result, hint)
				}
				// Branch guard: warn once per run when editing on a protected
				// branch (main, master, develop, release/*).
				if hint := a.checkBranchGuard(); hint != "" {
					a.appendGuidance(&result, hint)
				}
			}
		}
		// Consecutive edit failure recovery: when an edit fails on a file
		// 2+ times in a row, inject targeted guidance to re-read the file
		// before retrying. This catches the common "edit fail loop" pattern
		// faster than the overseer (which runs every 12 iterations).
		if result.IsError && fileEditingTools[tc.Name] {
			a.convergenceRecordEditError()
			for _, p := range extractEditFilePaths(tc.Name, tc.Arguments) {
				if hint := a.editFailRecovery.recordEditFailure(p); hint != "" {
					a.appendGuidance(&result, hint)
				}
			}
		}
		// Overcorrection cascade: record error signals for proportionality
		// analysis. Must run on the pristine result content - moved above
		// injectRulesIntoResult so injected learned-rule text can never
		// re-classify as a diagnostic error and self-trigger the recorder
		// (issue #1141).
		a.overcorrectionRecordError(tc.Name, result.Content, result.IsError)
		// #1823 case 2: a successful rollback tool after observed give-up
		// language completes the surrender pairing.
		if !result.IsError && giveupRollbackTools[tc.Name] {
			a.recordGiveupRollback()
		}
		// False premise detection: record tool errors for later contradiction
		// analysis against assistant success claims.
		//
		// Tool output integration monitor: extract high-signal tokens from
		// information-tool results for integration checking against the next
		// assistant text (TRACE cross-step evidence, issue #341).
		//
		// Both must run on the PRISTINE result content - moved above
		// injectRulesIntoResult so injected learned-rule text is never
		// recorded as an error snippet (its build-fail phrasing would poison
		// isBuildTestError and the #546/#593 supersede logic) nor mined as
		// "evidence" tokens the next assistant turn will never echo,
		// mirroring the same invariant as issue #1141 (issue #1165).
		a.falsePremise.recordToolResult(tc.Name, result.Content, result.IsError)
		a.integrationRecordToolResult(tc.Name, result.Content)
		result.Content = a.injectRulesIntoResult(tc.Name, tc.Arguments, result.Content)
		// Batch edit conflict warning: if this tool call targets a file that
		// is also edited by another call in the same batch, inject a warning
		// so the model understands why edits may fail and how to consolidate.
		if warn, ok := batchConflictWarnings[idx]; ok {
			a.appendGuidance(&result, warn)
		}
		if result.IsError {
			debug.Log("agent", "tool result ERROR: tool=%s output=%s", tc.Name, util.Truncate(result.Content, 200))
		}
		// Overcorrection cascade: increment step counter for non-edit tools
		// so stale errors expire (#104).
		if !isEditTool(tc.Name) {
			a.overcorrection.recordNonEditStep()
		}
		// Capability boundary: track consecutive tool failures for
		// stubborn-persistence detection.
		a.capBoundary.recordToolResult(result.IsError)
		// Argument size guard: detect oversized tool arguments (e.g., huge
		// old_text anchors in edit_file, massive write_file content) and
		// inject a context-efficiency hint. Fires at most once per run.
		if argSizeHint := a.checkArgSizeGuard(tc.Name, tc.Arguments); argSizeHint != "" {
			a.appendGuidance(&result, argSizeHint)
		}
		// Tool call sequence validator: detect cross-iteration anti-patterns
		// (e.g., full read then targeted re-read, sequential individual reads
		// instead of batch, list_directory then glob on same dir). Each
		// pattern type fires at most once per run.
		if seqHint := a.toolSequence.record(tc, iteration+1); seqHint != "" {
			a.appendGuidance(&result, seqHint)
		}
		// Orphaned background command tracking: record start_command jobs
		// and mark output checks. Detects forgotten background processes.
		a.recordBgToolCall(tc.Name, tc.Arguments, result.Content, iteration+1)
		// Action annihilation detection: check if this tool call cancels
		// a prior tool call's side effects (git_add→git_reset, edit→undo, etc.).
		// #2675: failed calls have no side effects to cancel, yet were
		// recorded and matched - a failed checkout followed by error
		// recovery and a successful retry to the SAME branch fired a bogus
		// "branch thrashing" warning at the most critical moment (error
		// recovery). Mirrors the #1459-A lesson applied to fragmentation.
		if !result.IsError {
			if annihilWarn := a.actionAnnihil.recordToolCall(tc.Name, tc.Arguments, iteration+1); annihilWarn != "" {
				debug.Log("agent", "Iteration %d: action annihilation detected", iteration+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: annihilWarn}},
				})
			}
		}
		// Exploration fragmentation detection: check if the agent is
		// issuing many scattered exploration calls without converging.
		// #1459-A: failed calls (missing args etc.) don't count - two
		// errored reads plus four good ones used to trip the detector
		// with the failures' 60-char fallback blobs as fake targets.
		// #1559-B: the gate must only exclude EXPLORATION COUNTING -
		// wrapping the whole recordToolCall also disabled the mutating
		// window reset, so a FAILED run_command (a converging action
		// being handled) no longer reset the window and the very next
		// read re-fired "without any converging action (edit, write,
		// command)" right after the agent had run a command.
		fragWarn := ""
		if !result.IsError || mutatingToolNamesFrag[tc.Name] {
			fragWarn = a.exploreFrag.recordToolCall(tc.Name, tc.Arguments, iteration+1)
		}
		if fragWarn != "" {
			debug.Log("agent", "Iteration %d: exploration fragmentation detected", iteration+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: fragWarn}},
			})
		}
		// Orphaned new file detection: check if new source files were
		// created but never integrated via edits to existing files.
		// #1587-B: FAILED writes (sandbox rejection, batch abort) never
		// created anything - tracking them made "file(s) created" fire
		// with text that lied about disk state. Gate on success.
		if !result.IsError {
			if orphanWarn := a.orphanFile.recordToolCall(tc.Name, string(tc.Arguments), iteration+1); orphanWarn != "" {
				debug.Log("agent", "Iteration %d: orphaned file detected", iteration+1)
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: []provider.ContentBlock{{Type: "text", Text: orphanWarn}},
				})
			}
		}
		// Build idempotency detection: check if a deterministic build/test
		// command is being re-run with 0 source edits since the last build.
		if idempWarn := a.buildIdempot.recordToolCall(tc.Name, tc.Arguments, iteration+1); idempWarn != "" {
			debug.Log("agent", "Iteration %d: build idempotency violation detected", iteration+1)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: idempWarn}},
			})
		}
		// Tainted data influence detection (IFC): check if untrusted content
		// from prior tool outputs has flowed into the arguments of this
		// privileged tool call. Warns when tainted content influences
		// write/exec operations. Research: Microsoft IFC (arXiv:2505.23643).
		if taintWarn := a.taintInfluence.checkInfluence(tc.Name, string(tc.Arguments)); taintWarn != "" {
			debug.Log("agent", "Iteration %d: tainted data influence detected on %s", iteration+1, tc.Name)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: taintWarn}},
			})
		}
		// Tool call storm tracking: record each tool call to detect
		// diverse-tool bursts without interleaved reasoning.
		a.serialRead.recordToolCall(tc.Name)
		a.reasoningRedund.recordReasoning("", true) // tool call breaks text-only streak
		// Verification coverage gap: detect edits across multiple packages
		// but verification command only covers a subset.
		if covWarn := a.editCoverage.recordToolCall(tc.Name, string(tc.Arguments)); covWarn != "" {
			debug.Log("agent", "Iteration %d: verification coverage gap detected", iteration+1)
			// #1821 case 3: scopeNarrow's message for the SAME command
			// is near-identical - let it skip once instead of double-
			// injecting on one tool call.
			if cmd := extractCommandFromToolCall(tc.Arguments); cmd != "" {
				a.scopeNarrow.lastCoverageWarnedCmd = cmd
			}
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: covWarn}},
			})
		}
		// Tool effectiveness tracking: monitor per-tool success rates.
		// When a tool repeatedly errors or yields poor results (empty
		// searches, truncated output, edit rejections), inject guidance
		// suggesting alternative tools or approaches.
		if effGuidance := a.toolEff.recordCall(tc.Name, result.Content, result.IsError); effGuidance != "" {
			debug.Log("agent", "Iteration %d: tool effectiveness guidance for %s", iteration+1, tc.Name)
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: effGuidance}},
			})
		}
		// Redundant re-verification: detect same verification command
		// re-run without intervening file edits (idempotency violation).
		if rvHint := a.redundantReverify.recordToolCall(tc.Name, string(tc.Arguments), iteration+1, result.IsError); rvHint != "" {
			debug.Log("agent", "Iteration %d: redundant re-verification detector triggered", iteration+1)
			a.injectGuidance(rvHint)
		}
		// Outcome misattribution: record tool calls to track corrective
		// actions between failure and success claim.
		a.outcomeMisattrib.recordToolCallForOM(tc.Name)
		// Verification disconnect: record result to detect failures
		// that get advanced past without resolution.
		// Narrative-evidence decoupling: record tool results to detect
		// contradictions between agent text claims and actual outputs.
		// Delayed observation contradiction: record negative observations from
		// *successful* tool calls (no-match, not-found, empty) for later
		// delayed contradiction analysis.
		// Belief defense escalation: record tool results to detect
		// contradicting signals against earlier agent beliefs.
		// Bridging rationalization: record tool results to detect
		// contradictions that may be explained away in subsequent text.
		// History error accumulation: track multi-issue tool outputs
		// to detect partial acknowledgment patterns.
		// Verification scope decay: record verification commands to
		// detect progressive narrowing of test/build scope.
		// Outcome misattribution: record failure indicators in tool
		// results to detect success claims that follow failures.
		a.outcomeMisattrib.recordResult(tc.Name, result.Content, result.IsError, iteration+1)
		// Context-length goal drift: record tool call targets to
		// detect drift from original user request (arXiv:2505.02709).
		a.goalDriftCtx.recordToolCall(tc.Name, string(tc.Arguments))
		// Foresight calibration: compare predicted outcome against
		// actual result to detect prediction-observation mismatches.
		if fcHint := a.foresightCalib.checkCalibration(tc.Name, result.Content, result.IsError, iteration+1); fcHint != "" {
			debug.Log("agent", "Iteration %d: foresight calibration detector triggered", iteration+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: fcHint,
				}},
			})
		}
		// Self-declared constraint violation: check if this tool call
		// violates constraints the agent declared in its own reasoning.
		if cvMsg := a.constraintViolation.checkToolCall(tc.Name, parseToolArgs(tc.Arguments), iteration+1); cvMsg != "" {
			debug.Log("agent", "Iteration %d: constraint violation detector triggered", iteration+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: cvMsg,
				}},
			})
		}
		// Error strategy loop: track error categories across all tool
		// calls to detect systemic approach failures (procedural memory
		// gap from ProcMEM arXiv:2602.01869).
		a.errStrategyLoop.recordResult(result.Content, result.IsError)
		// Strategy exhaustion: track diverse recovery strategies failing
		// for the same error (EEA robustness entropy, MiRA subgoal decomposition).
		if seMsg := a.strategyExhaustion.recordToolCall(tc.Name, result.IsError, result.Content, iteration+1); seMsg != "" {
			debug.Log("agent", "Iteration %d: strategy exhaustion detector triggered", iteration+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: seMsg,
				}},
			})
		}
		// Solution fixation: track failed edit attempts per file to
		// detect diagnosis anchoring (arXiv:2505.15392, arXiv:2509.25370).
		// #639: every tool call advances the sliding window (the unit is
		// "12 tool calls", not "12 edits"); only failed mutation edits
		// feed the per-file counts (handled inside recordToolCall).
		a.solutionFixation.recordToolCall(tc.Name, string(tc.Arguments), result.IsError)
		// #1486 case E: a FAILED edit_file/write_file changed nothing on
		// disk - counting it as editsSince wrongly told the reverify
		// detector "sources changed since your last verify" and
		// suppressed a legitimate redundant-rerun warning.
		if !result.IsError {
			a.redundantReverify.recordEdit(tc.Name)
		}
		if fixationHint := a.solutionFixation.checkAndWarn(); fixationHint != "" {
			debug.Log("agent", "Iteration %d: solution fixation detector triggered", iteration+1)
			a.injectGuidance(fixationHint)
		}
		// Unverified self-diagnosis: record tool results to track errors
		// and verification calls for correlated failure detection.
		if strategyHint := a.errStrategyLoop.checkAndWarn(); strategyHint != "" {
			debug.Log("agent", "Iteration %d: error strategy loop detector triggered", iteration+1)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: strategyHint,
				}},
			})
		}
		// Temporal blindness: track verification results and mutations
		// to detect stale verification claims after code changes.
		// Wasted exploration tracking: record search tool results and
		// file path consumption. Detects searches whose results were
		// never acted upon.
		// Self-modification safety: check write tool calls for targets
		// that modify the agent's own infrastructure (config, memory,
		// hooks, permissions, system prompts).
		if selfModMsg := a.checkSelfModification(tc.Name, tc.Arguments); selfModMsg != "" {
			a.contextManager.Add(provider.Message{
				Role:    "user",
				Content: []provider.ContentBlock{{Type: "text", Text: selfModMsg}},
			})
		}
		// Information scent tracking: record exploration calls and their
		// path novelty to detect depleted information patches.
		a.infoScent.recordExploration(tc.Name, string(tc.Arguments), result.Content, iteration+1)
		// Query convergence tracking: record search queries and code
		// actions to detect repeated similar searches without progress.
		a.queryConverge.recordToolCall(tc.Name, string(tc.Arguments), iteration+1)
		// Plan drift capture: when exit_plan_mode fires, extract plan items
		// for later drift detection (spec-driven development tracking).
		if tc.Name == "exit_plan_mode" {
			a.planDrift.capturePlan(extractPlanFromArgs(tc.Arguments))
		}
		// Delegation orchestration: track spawned agents and result consumption.
		if a.delegationOrch != nil {
			if delegationToolNames[tc.Name] {
				taskSum := extractDelegationTaskSummary(tc.Name, tc.Arguments)
				a.delegationOrch.recordDelegationCall(tc.ID, tc.Name, taskSum, result.Content, iteration+1)
			} else if delegationResultTools[tc.Name] && !result.IsError {
				// Only successful result checks count as consumption; a failed
				// wait/task_output did not actually retrieve anything.
				a.delegationOrch.recordResultCheck(tc.Name, tc.Arguments, result.Content, iteration+1)
			}
			a.delegationOrch.recordToolCallCount()
		}
		// Record tool errors for reflection/ratchet rule extraction.
		if result.IsError {
			runStats.recordToolError(tc.Name, result.Content)
		}
		// Silent error advancement detection: track when errors go unaddressed.
		if result.IsError {
			rKey := extractErrorResourceKey(tc.Name, tc.Arguments)
			a.silentError.recordToolError(tc.Name, rKey, result.Content, iteration+1)
		} else {
			rKey := extractErrorResourceKey(tc.Name, tc.Arguments)
			if silentMsg := a.silentError.recordToolAction(tc.Name, rKey); silentMsg != "" {
				a.appendGuidance(&result, silentMsg)
			}
		}
		if tc.Name == "run_command" || tc.Name == "start_command" {
			cmd := extractCommandFromToolCall(tc.Arguments)
			if cmd != "" {
				// Verification scope narrowing: detect progressively narrowing
				// test/build commands that mask failures (command-level spec gaming).
				if narrowMsg := a.scopeNarrow.recordVerificationCommand(tc.Name, cmd, result.Content, result.IsError); narrowMsg != "" {
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: []provider.ContentBlock{{Type: "text", Text: narrowMsg}},
					})
				}
			}
		}
		// Record tool result for adaptive effort classification.
		if a.effortAdapter != nil {
			a.effortAdapter.recordToolResultErr(tc.Name, result.IsError, result.Content)
		}
		// Record tool result for adaptive sampling classification.
		// #2636: pass errText so sampling applies the same
		// error-recovery filtering as the effort adapter above.
		if a.adaptiveSampling != nil {
			a.adaptiveSampling.recordToolResultErr(tc.Name, result.IsError, result.Content)
		}
		// Strategy stagnation detector: tracks same-tool+target retries
		// after failure. When 2+ consecutive failures with identical
		// approach occur, inject guidance to pivot strategy.
		if a.strategyStagnation.recordAttempt(tc.Name, string(tc.Arguments), !result.IsError) {
			debug.Log("agent", "Iteration %d: strategy stagnation detected (tool=%s)", iteration+1, tc.Name)
			a.contextManager.Add(provider.Message{
				Role: "user",
				Content: []provider.ContentBlock{{
					Type: "text",
					Text: strategyStagnationWarning(tc.Name, extractStagnationTarget(tc.Name, string(tc.Arguments)), stagnationFailureThreshold),
				}},
			})
		}
		// #952: capture the ORIGINAL content length BEFORE the detector chain
		// (errorClassifier at errorClassifier below through consensus). Guidance
		// appended by detectors flows into token-waste metering at record time —
		// metering the polluted string double-counts guidance tokens in both the
		// waste numerator and denominator (#553 residual: the old capture point
		// sat after the chain, so an original 1-token result was recorded as 19).
		// #1819 case 2: metering now uses measuredLen captured post-shrink /
		// pre-hint at the applyToolResultGuidance call site; nothing needs the
		// pre-detector-chain length anymore.
		// Error classifier: immediate type-specific guidance on the first
		// occurrence of each error category (AgentDebug-inspired).
		// Fires before error-streak so the agent gets targeted feedback
		// immediately, not after 4 consecutive failures.
		if result.IsError {
			if catGuidance := a.errorClassifier.classifyToolError(tc.Name, result.Content); catGuidance.Name != "" {
				g := fmt.Sprintf("[Error guidance: %s] %s", catGuidance.Name, catGuidance.Guidance)
				a.appendGuidance(&result, g)
			}
		}
		// Tool error fallback chain: on tool failure, inject actionable
		// alternative strategy suggestions. Fires once per tool per run.
		if result.IsError {
			if fallbackHint := a.toolFallbackCheck(tc.Name, result.Content); fallbackHint != "" {
				a.appendGuidance(&result, fallbackHint)
			}
		}
		// Shell-to-native tool suggestion: when the agent uses run_command
		// for something a native tool does better (cat, grep, git log, etc.),
		// suggest the native tool for richer output and better integration.
		if nativeHint := a.shellNativeHint.maybeShellNativeHint(tc.Name, tc.Arguments); nativeHint != "" {
			a.appendGuidance(&result, nativeHint)
		}
		// Error-streak detection: if consecutive tool calls are failing,
		// inject strategic guidance to break the cycle.
		if errorGuidance := a.errorStreakCheck(result.IsError, tc.Name); errorGuidance != "" {
			a.appendGuidance(&result, errorGuidance)
		}
		// Compounding failure detection: sliding-window cross-tool failure
		// rate analysis. Catches interleaved fail-succeed-fail patterns that
		// consecutive-error detection cannot (any success resets the streak).
		a.compoundingFailure.recordResult(tc.Name, result.IsError)
		if compoundingGuidance := a.compoundingFailure.check(); compoundingGuidance != "" {
			a.appendGuidance(&result, compoundingGuidance)
		}
		// Diagnostic-action disconnect detection: when a tool result contains
		// diagnostic content (errors, undefined symbols, etc.), track whether
		// subsequent actions address it. Fires after N disconnected actions.
		// Tool output integration monitoring: extract high-signal tokens
		// from information-retrieval tool results (file paths, symbols, line
		// numbers) and check if they appear in the next assistant text.
		// (TRACE-inspired cross-step evidence tracking).
		// Tool output claim verification: detect commonly misread failure
		// signals in nominally-successful tool results (AgentRx-inspired).
		// Catches "exit code 1" in output, panics, "no results", etc. that
		// IsError does not capture, preventing the agent from claiming
		// success when the tool output contradicts that interpretation.
		if claimGuidance := a.claimVerify.check(tc.Name, result.Content, result.IsError, extractCommandFromToolCall(tc.Arguments)); claimGuidance != "" {
			a.appendGuidance(&result, claimGuidance)
		}
		// Permission-deny streak guard: a run of consecutive policy denials
		// usually means the agent is operating in an unintended permission
		// mode (e.g. dropped into plan mode without realizing it). Name the
		// mode and the self-rescue path instead of letting the model burn
		// context on denied retries (#1210).
		if modeGuard := a.permDenyStreak.record(a.currentMode(), result); modeGuard != "" {
			a.appendGuidance(&result, modeGuard)
		}
		// Failure mode classification: meta-level strategy guidance.
		// Classifies each error into transient/structural/systemic and
		// injects high-level strategy when a dominant mode emerges.
		if modeGuidance := a.failureMode.recordResult(tc.Name, result.IsError, result.Content); modeGuidance != "" {
			// #952: this guidance is appended BEFORE the consensus scan window
			// starts (see consensusBaseline), so record the firing explicitly —
			// the content scan below would otherwise never see this detector.
			a.crossDetectorConsensus.recordFiring("Failure Mode", iteration+1)
			a.appendGuidance(&result, modeGuidance)
		}
		// Error cascade detection: when multiple errors share a common root
		// resource (file path or symbol), inject root-cause-first guidance.
		if result.IsError {
			if cascadeGuidance := a.errorCascade.recordError(tc.Name, result.Content); cascadeGuidance != "" {
				// #952: same as failureMode above — this guidance precedes the
				// consensus scan window; record the firing explicitly.
				a.crossDetectorConsensus.recordFiring("Error Cascade", iteration+1)
				a.appendGuidance(&result, cascadeGuidance)
			}
		}
		// Error propagation chain: detect degraded (non-error but empty/
		// truncated/null) outputs that subsequent steps build on without
		// verifying, causing silent compounding failures.
		if propGuidance := a.errorPropagate.recordResult(tc.Name, result.Content, result.IsError); propGuidance != "" {
			a.appendGuidance(&result, propGuidance)
		}
		// Scope drift: track productive file edits for semantic scope creep.
		// #1491: gate on success like the sibling driftRecurrenceRecord
		// below and the #495/#953 pattern at 4067 - failed edits (old_text
		// mismatch, denied) never changed anything and must not inflate
		// productiveCount/editFiles/editedDirs.
		if !result.IsError {
			a.scopeDriftRecord(tc.Name, extractFileHint(tc.Name, tc.Arguments))
		}
		// Drift recurrence: track edits and verifications relative to any drift warning.
		a.driftRecurrenceRecord(tc.Name, extractFileHint(tc.Name, tc.Arguments), string(tc.Arguments), !result.IsError)
		// Last-known-good checkpoint: track edits for revert targeting.
		// #1581-B: FAILED edits (bad old_text, wrong path) never touched
		// disk - recording them put GHOST files into the revert list,
		// and formatRevertGuidance suggested removing paths that never
		// existed. Gate on success like the sibling trackers.
		// #1762 case 1: multi_file_edit partial_success sets IsError=true
		// for the WHOLE result, but the files in written_paths ARE on
		// disk (atomicWriteFile per plan). The whole-result gate excluded
		// them from the revert list - the opposite distortion of #1581
		// (real modifications missing from revertGuidance). Record those
		// per-file. Also closes the multi-file Info gap: a successful
		// multi-file edit used to record only extractFileHint's first path.
		if written := extractWrittenPaths(result.Content); len(written) > 0 {
			for _, p := range written {
				a.lastGoodCheckpointRecordEdit(tc.Name, p)
			}
		} else if !result.IsError {
			a.lastGoodCheckpointRecordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments))
		}
		// Monorepo scoper: track which packages are being edited.
		if fh := extractFileHint(tc.Name, tc.Arguments); fh != "" {
			a.monorepoScoper.recordEdit(fh)
		}
		if scopeGuidance := a.scopeDriftCheck(); scopeGuidance != "" {
			// Mark that a drift warning fired, so drift recurrence can track behavior.
			a.driftRecurrenceMarkWarn(runStats.Iterations)
			a.appendGuidance(&result, scopeGuidance)
		}
		// Drift recurrence: check if the agent continued the warned pattern.
		if recurrenceGuidance := a.driftRecurrenceCheck(); recurrenceGuidance != "" {
			a.appendGuidance(&result, recurrenceGuidance)
		}
		// Overseer: deterministic trajectory analysis (SICA-inspired).
		// Detects tool spam, read-only stall, stuck-on-file, error escalation, and drift.
		if overseerGuidance := a.overseerCheck(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments), runStats.Iterations); overseerGuidance != "" {
			a.appendGuidance(&result, overseerGuidance)
		}
		// Repetition tracker: semantic-level detection of failed edit clusters.
		// Catches near-miss loops that exact-match loop detection misses.
		if repetitionGuidance := a.repetitionCheckEdit(tc.Name, tc.Arguments, result.IsError); repetitionGuidance != "" {
			a.appendGuidance(&result, repetitionGuidance)
		}
		// Also check read-edit-fail cycles for read_file calls.
		if tc.Name == "read_file" || tc.Name == "multi_file_read" {
			if readGuidance := a.repetitionCheckRead(extractFileHint(tc.Name, tc.Arguments)); readGuidance != "" {
				a.appendGuidance(&result, readGuidance)
			}
		}
		// Trajectory confidence: record result and check for early warning.
		// HTC-inspired: detect "overconfidence in failure" before errors compound.
		// Causal attribution: record edit steps for failure root-cause tracing.
		a.causalAttribution.recordEdit(tc.Name, extractFileHint(tc.Name, tc.Arguments), iteration)
		a.confidence.recordResult(tc.Name, result.IsError, extractFileHint(tc.Name, tc.Arguments))
		// Causal attribution: on failures, trace backward to the likely causal edit.
		// #1442-A: gate by TOOL NAME too - the old path ran on EVERY tool's
		// result, and a multi-line grep/read_file output (path.go:line:content
		// is character-for-character the error-file regex's shape) with a
		// stray failure word blamed an INNOCENT edit (probe: CRS=84 on a
		// passing-test grep). Only command/test channels carry build output.
		// #1528: read_command_output is the polling channel for
		// start_command jobs (the tool docs route completion reads
		// through it) - long-test workflows surface failures there,
		// not in wait_command. Without it the detector stayed silent
		// on the most common background-test failure path.
		if tc.Name == "run_command" || tc.Name == "bash" || tc.Name == "powershell" || tc.Name == "start_command" || tc.Name == "wait_command" || tc.Name == "read_command_output" {
			if result.IsError || looksLikeFailure(result.Content) {
				// #1528 case C: pass the command text and exit status - a
				// succeeded grep/cat of logs carrying "FAIL" must not be
				// attributed as a build failure (shell bypasses the
				// layer-1 tool-name filter).
				if causalHint := a.causalAttribution.attributeFailureCmd(result.Content, causalCmdForGate(tc, result.Content), result.IsError); causalHint != "" {
					a.appendGuidance(&result, causalHint)
				}
			}
		}
		if confidenceGuidance := a.confidence.maybeIntervene(); confidenceGuidance != "" {
			a.appendGuidance(&result, confidenceGuidance)
		}
		// Verification debt: track unverified modifications (SAUP-inspired).
		// Detects when the agent stacks edits without building/testing.
		a.verifDebt.recordToolCall(tc.Name, string(tc.Arguments))
		// Undo-blind moved to pre-execution (#1799 case 1) - see the
		// loop above; the hint rides the tool result there.
		// Premature commitment: record exploratory actions to track
		// evidence gathering before the first edit.
		a.prematureCommit.recordExploration(tc.Name, extractFileHints(tc.Name, tc.Arguments))
		if debtGuidance := a.verifDebt.maybeWarn(); debtGuidance != "" {
			a.appendGuidance(&result, debtGuidance)
		}
		// #1454-A: this record call was accidentally dropped by 31a79906
		// (its diff replaced editAbandon.recordToolCall with the undoBlind
		// call) - maybeWarn stayed wired but the state was forever empty:
		// the detector never fired once since birth. Restored.
		a.editAbandon.recordToolCall(tc.Name, string(tc.Arguments))
		if abandonGuidance := a.editAbandon.maybeWarn(); abandonGuidance != "" {
			a.appendGuidance(&result, abandonGuidance)
		}
		// File churn detection: track repeated edits to the same file.
		// Each re-edit signals an invalidated assumption about the file.
		if isEditTool(tc.Name) {
			a.fileChurn.recordEdit(extractEditedPaths(tc))
			for _, p := range extractEditedPaths(tc) {
				a.tunnelVision.recordFile(p)
			}
			// Premature commitment detection: check evidence sufficiency
			// at the first edit. ECLoop (arXiv:2607.28815) shows that
			// editing before gathering sufficient context (callers, tests,
			// related code) leads to incorrect patches in 20-27% of cases.
			pcMsg := a.prematureCommit.checkFirstEdit(extractEditedPaths(tc))
			if pcMsg != "" {
				a.appendGuidance(&result, pcMsg)
			}
			if cg := func() string {
				if !shouldRunDetector(detectorTierRoutine, iteration+1) {
					return ""
				}
				return a.fileChurn.check()
			}(); cg != "" {
				a.appendGuidance(&result, cg)
			}
			// Edit oscillation detection: track content signature reversals.
			// Convergence Detection (agentpatterns.ai, 2026) identifies
			// oscillation as a critical failure pattern where the agent
			// alternates between two versions without resolving trade-offs.
			a.editOscillation.recordEdit(tc.Name, tc.Arguments, iteration+1)
			if om := func() string {
				if !shouldRunDetector(detectorTierRoutine, iteration+1) {
					return ""
				}
				return a.editOscillation.check()
			}(); om != "" {
				a.appendGuidance(&result, om)
			}
		}
		// Tunnel vision detection: warn when the agent has done many
		// iterations but only touched a few files (under-exploration).
		// Coppersun.dev 2026: "context window holds 1-2 files; bugs span 3+"
		if tv := func() string {
			if !shouldRunDetector(detectorTierRoutine, iteration+1) {
				return ""
			}
			return a.tunnelVision.check(runStats.Iterations)
		}(); tv != "" {
			a.appendGuidance(&result, tv)
		}
		// Tunnel vision detection: warn when the agent has done many
		// iterations but only touched a few files (under-exploration).
		// Agentic abstention detection: track negative environment signals
		// (not found, unavailable) to detect untimely continuation.
		// Silent degradation propagation: record degraded tool results for
		// later acknowledgment check against assistant text.
		// Smart verify hint reset: if the agent ran a build/test/verify command,
		// reset the edit counter and track the result.
		a.maybeResetVerifyOnCommand(tc.Name, tc.Arguments, result.IsError)
		// #1549: gate by command CONTENT like every sibling (#1455-A's
		// maybeResetVerifyOnCommand above, #487's propagation counter).
		// The unconditional call made ANY successful tool - read_file,
		// grep, even the successful edit itself (clearing right before
		// recordSourceEdit adds 1 back) - zero the debt, so debt never
		// exceeded 1 and the warn thresholds (7/12) were unreachable:
		// the detector was permanently silent.
		if tc.Name == "run_command" && !result.IsError && isVerificationCommand(extractCommandFromArgs(tc.Arguments)) {
			a.verifyDebt.recordVerifyCommand(extractCommandFromArgs(tc.Arguments), result.IsError)
		}
		// #487: gate on command CONTENT — the unconditional raw setter made
		// the first read_file count as a build/test and silenced the
		// detector for the whole run.
		a.prematureRefactorRecordVerifyForTool(tc.Arguments)
		// #1455-A: gate on command CONTENT, exactly like the #487 fix two
		// lines above - the unconditional !IsError reset made ANY
		// successful tool result (read_file/grep, even the successful
		// edit_file itself) clear the distinct-file set, and since this
		// runs BEFORE recordEdit, the set size stayed <=1 and the
		// detector's own charter ("7 edits to 7 DIFFERENT files") was
		// unreachable. Only a successful VERIFY COMMAND resets now.
		if tc.Name == "run_command" && !result.IsError {
			if cmd := extractCommandFromArgs(tc.Arguments); cmd != "" && isVerifyCommand(cmd) {
				a.editPropagation.recordGreenBuild()
				// #1460-C: a green verification confirms the edit
				// sequence was legitimate refinement, not churn.
				// #1561 case B: only failure-aware verification (test/
				// build/vet-class) may clear, and the clear is scoped
				// to the command's path arguments - `gofmt -l .` (green
				// while REPORTING problems), `make clean` and
				// scope-unrelated commands no longer wipe the books.
				if isStrictVerifyCommand(cmd) {
					a.fileChurn.recordVerifySuccess(cmd)
				}
			}
		}
		// Convergence lock: record verification result to detect post-verify
		// unnecessary edit drift. A successful verify arms the lock; a failed
		// verify disarms it (agent is legitimately fixing issues).
		a.convergenceRecordVerify(tc.Name, tc.Arguments, result.IsError)
		// Recurring error detection: when a build/test command returns the
		// SAME error after file edits, inject guidance that the edits aren't
		// addressing the root cause. This catches the #1 agent failure mode
		// (incremental edits that don't fix the underlying problem).
		if recurringGuidance := a.recurringErrorCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); recurringGuidance != "" {
			a.appendGuidance(&result, recurringGuidance)
		}
		// Fix cascade detection: tracks edit->verify->fail cycles regardless
		// of specific errors. Detects wrong-hypothesis lock-in where each
		// edit produces a DIFFERENT error (so recurring_error never fires).
		// Stalled convergence: track error counts across verifications
		// to detect diminishing returns (convergence plateau).
		if stalledGuidance := a.stalledConvergenceCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); stalledGuidance != "" {
			a.appendGuidance(&result, stalledGuidance)
		}
		if regressionGuidance := a.errorRegressionCheckCommand(tc.Name, tc.Arguments, result.Content, result.IsError); regressionGuidance != "" {
			a.appendGuidance(&result, regressionGuidance)
		}
		// Error compounding risk: track all error signals across the run.
		// Computes geometric compounding probability to detect systemic risk.
		if hadError := a.errorCompound.recordResult(tc.Name, result.IsError, iteration+1); true {
			a.errorCompound.recordStep(hadError)
		}
		// Fix amnesia: track errors observed and check new content for recurrence.
		if result.IsError {
			if cat, file := classifyToolError(tc.Name, result.Content); cat != "" {
				a.fixAmnesia.recordErrorObserved(cat, file)
			}
		}
		if csIsEditTool(tc.Name) || tc.Name == "write_file" || tc.Name == "multi_edit_file" {
			fp := extractFilePathFromArgs(tc.Name, tc.Arguments)
			if result.IsError {
				// Error observed in this file - track it.
				if cat, file := classifyToolError(tc.Name, result.Content); cat != "" {
					if file == "" {
						file = fp
					}
					a.fixAmnesia.recordErrorObserved(cat, file)
				}
			} else {
				// #754: successful edit promotes observed errors in this
				// file to FIXED (observe->fix two-phase wiring; previously
				// recordErrorObserved alone marked categories "fixed" with
				// no edit ever happening).
				a.fixAmnesia.recordFileEdited(fp)
			}
			// Check new content for patterns matching previously-fixed errors.
			if faGuidance := a.fixAmnesia.checkContentAgainstFixed(extractFilePathFromError(result.Content), fp, result.Content); faGuidance != "" {
				a.appendGuidance(&result, faGuidance)
			}
		}
		// Correction spiral: track edits and verify results to detect
		// error severity escalation across fix attempts. Only genuine
		// verification commands feed the sequence (#491): a successful
		// cat/ls between edit and build used to break the correction
		// chain (detector permanently blind) and failed exploratory
		// commands polluted the severity sequence. start_command is
		// excluded entirely: its result reflects job startup, not the
		// verification outcome.
		var psArgs map[string]interface{}
		if len(tc.Arguments) > 0 {
			_ = json.Unmarshal(tc.Arguments, &psArgs)
		}
		if csIsEditTool(tc.Name) {
			a.correctionSpiral.recordEdit(iteration + 1)
		} else if tc.Name == "run_command" && psIsVerifyCommand(sfCommandArg(psArgs)) {
			a.correctionSpiral.recordVerifyResult(tc.Name, result.Content, result.IsError, iteration+1)
		} else if tc.Name == "wait_command" || tc.Name == "read_command_output" || tc.Name == "task_output" {
			// #1773 case 4: the final outcome of a long verification
			// run arrives HERE - wait_command returns the job's terminal
			// result, yet the run_command-only wiring left the most
			// informative evidence out of the correction spiral. Reuse
			// the #1153 registry: attribute only jobs registered as
			// verification (start_command itself still excluded -
			// launch is not an outcome).
			// #1880 case 2: these tools also legally return NON-terminal
			// results (poll timeout -> "Still running..." with
			// IsError=false). premature_success filters those with
			// psTerminalVerifyOutcome before grading; without the same
			// gate here, a partial output counted as GREEN and RESET
			// the spiral (a green run clears pendingEdit+errorSequence),
			// blinding the detector when the job later genuinely failed.
			jobID, _ := psArgs["job_id"].(string)
			if jobID == "" {
				jobID, _ = psArgs["task_id"].(string)
			}
			if jobID != "" && a.prematureSuccess.psJobIsVerify(jobID) {
				if terminal, _ := psTerminalVerifyOutcome(psParseJobStatus(result.Content)); terminal {
					a.correctionSpiral.recordVerifyResult(tc.Name, result.Content, result.IsError, iteration+1)
				}
			}
		}
		// Unverified mutation streak: track consecutive edits without verification.
		a.bareEditStreak.recordToolCall(tc.Name, string(tc.Arguments))
		// Green build illusion: track source modifications, builds, and tests.
		// Premature success claim: track edits and verification commands.
		a.prematureSuccess.recordToolCall(tc.Name, psArgs, result.IsError, result.Content) // #1153
		// Phantom verification: track which verification categories were
		// actually run, ignoring failures (issue #593 P3).
		a.phantomVerify.recordToolCall(tc.Name, string(tc.Arguments), result.IsError)
		// Strategy fixation: track per-file edits and verification outcomes.
		// Only genuine verification commands count: a successful cat/ls/
		// git status must not reset streaks, and a failed one must not
		// inject a bogus failure (#485). Filter through the same
		// command-position analysis as premature_success (#483).
		sfIsVerify := strategyFixationIsVerification(tc.Name)
		if sfIsVerify && (tc.Name == "run_command" || tc.Name == "start_command") {
			sfIsVerify = psIsVerifyCommand(sfCommandArg(psArgs))
		}
		if strategyFixationIsMutation(tc.Name) {
			// Record EVERY referenced file: multi_file_edit edits all
			// files[] entries and notebook_edit carries notebook_path —
			// counting only the first path under-tracked edits (#485).
			for _, fp := range sfExtractMutationPaths(tc.Arguments) {
				a.strategyFixation.recordEdit(fp)
			}
		} else if sfIsVerify {
			a.strategyFixation.recordVerification(tc.Name, result.Content, result.IsError)
		}
		// Error rush: track error-to-action dynamics for panic coding detection.
		a.errorRush.recordToolCall(tc.Name, result.Content, result.IsError)
		// Attention fragmentation: track directory switches for CLT extraneous load detection.
		var afArgs map[string]interface{}
		if len(tc.Arguments) > 0 {
			_ = json.Unmarshal(tc.Arguments, &afArgs)
		}
		a.attentionFragment.recordToolCall(tc.Name, afArgs)
		// Reckless execution: track exploration vs edit targets.
		if a.recklessExec != nil {
			argsStr := string(tc.Arguments)
			if recklessIsExplorationTool(tc.Name) {
				a.recklessExec.recordReadTool(tc.Name, argsStr)
			} else if recklessIsEditTool(tc.Name) {
				a.recklessExec.iteration = iteration + 1
				if a.recklessExec.recordEditTool(tc.Name, argsStr) {
					msg := recklessWarning(a.recklessExec.unexplored)
					a.contextManager.Add(provider.Message{
						Role:    "user",
						Content: []provider.ContentBlock{{Type: "text", Text: msg}},
					})
				}
			}
		}
		// #1776 case 3 / #1877: a FAILED verification is not grounding.
		// recordAction must run FIRST so this call's ledger entry exists
		// when the outcome is checked - the original wiring called
		// recordOutcome ~350 lines earlier, so the revoke always
		// inspected the PREVIOUS call's entry (revoking a success and
		// keeping the failure, or missing the failure entirely when an
		// interleaved read changed the tail).
		if a.irrevGate != nil {
			a.irrevGate.recordOutcome(tc.Name, result.IsError)
		}
		// Futile cycle: track reads vs writes to detect circular exploration.
		// #953: a FAILED edit produced no state mutation — recording it as a
		// write resets the epoch, so the legitimate re-read the agent performs
		// to recover a correct anchor (edit_fail_recovery's own advice) looks
		// like a futile re-read loop (Jaccard=1.0). Only successful edits count,
		// matching the !result.IsError style of the checks below (#495).
		if fileEditingTools[tc.Name] && !result.IsError {
			a.futileCycle.recordWrite()
			// #1598-A: a successful edit invalidates every prior
			// verification - the session-level everRun exemption (#1478-A)
			// must not outlive the edits it vouches for, or a fresh
			// "all tests pass" claim after editing 5 files stays
			// permanently exempt (the detector's core false-completion
			// target). Legal back-references WITHOUT intervening edits
			// keep the exemption.
			a.phantomVerify.invalidateEdits()
		} else if !result.IsError {
			// #1619-C: failed tools never touched the files they named -
			// a failed edit recorded its target as READ, so the legitimate
			// recovery re-read (edit_fail_recovery's own advice) inflated
			// the futile/expired signals - the read-side mirror of the
			// #953 write-side fix above.
			if readPaths := extractFilePathsFromArgs(tc.Arguments, tc.Name); len(readPaths) > 0 {
				// #500: batch-aware — multi_file_read carries N paths but the
				// old single-path extraction recorded only files[0], suppressing
				// the cross-file stale-read warning (minReadsBeforeWarning) and
				// starving expired-read/futile-cycle for the same reason.
				for _, p := range readPaths {
					a.futileCycle.recordRead(p)
					// Expired-read: track reads for self-invalidation detection.
					a.expiredRead.recordRead(p)
					a.wtInvalidation.recordRead(p)
				}
				// Post-edit re-read check: warn if re-reading shortly after edit.
				// First path only — each hint appends, N hints would spam.
				if hint := a.expiredRead.checkPostEditReread(readPaths[0]); hint != "" {
					a.appendGuidance(&result, hint)
				}
				// Search-result invalidation: record search-type tool results
				// for later invalidation detection on file edits.
				// #1491-A layer 1: this call sits OUTSIDE the readPaths
				// else-if - extractFilePathsFromArgs only knows path-carrying
				// argument keys (file/path/directory/...), so a repo-wide
				// grep with no explicit path (the most common form) starved
				// the detector; recordSearchResult carries its own
				// searchResultTools whitelist gate.
				a.searchInvalidation.recordSearchResult(tc.Name, result.Content)
			}
		}
		// Working-tree invalidation: detect cross-file stale reads after git mutations
		// #1527 case C: run_command carrying a mutating git command is
		// fed through the same invalidation path as the git_* tools.
		if (isWTMutatingTool(tc.Name) || (tc.Name == "run_command" && runCommandMutatesTree(string(tc.Arguments)))) && !result.IsError {
			if wtMsg := a.wtInvalidation.checkMutation(tc.Name, string(tc.Arguments)); wtMsg != "" {
				debug.Log("agent", "Iteration %d: working-tree invalidation detector triggered", iteration+1)
				a.appendGuidance(&result, wtMsg)
			}
		}
		if fileEditingTools[tc.Name] && !result.IsError {
			a.verifyDebt.recordSourceEdit()
			a.stalledConvergence.recordEdit()
			// Search-result invalidation: if edited file appeared in prior
			// search/lsp results, warn that those results are now stale.
			if editPath := extractFilePathFromArgs(tc.Name, tc.Arguments); editPath != "" {
				if siMsg := a.searchInvalidation.checkEditInvalidation(editPath); siMsg != "" {
					a.appendGuidance(&result, siMsg)
				}
			}
			a.editPropagation.recordEdit(tc.Name, string(tc.Arguments))
		}
		// #1499 case C: only SIDE-EFFECTING calls count as post-
		// declaration work - read-only exploration the agent announced
		// in the same breath ("Next, I'll check the tests") executed as
		// promised was tallied as "actions since declaring completion",
		// turning a component-level claim plus its announced wrap-up
		// into a false "premature declaration" accusation.
		if fileEditingTools[tc.Name] || tc.Name == "run_command" {
			a.successDeclare.recordToolCall()
		}
		a.subgoalTrack.recordToolCall(tc.Name, string(tc.Arguments))
		// Attempt brief: record outcome for knowledge reuse.
		a.attemptBrief.recordOutcome(tc.Name, extractToolTarget(tc.Name, string(tc.Arguments)), !result.IsError, iteration, result.Content)
		// Symbol grounding: record file paths and code identifiers from
		// tool I/O so we can detect ungrounded references later.
		// Tool result redundancy: detect when result content substantially
		// overlaps with a prior result still in context (AgentDiet waste).
		if trMsg := a.toolResultRedundancy.recordResult(tc.Name, result.Content, iteration+1); trMsg != "" {
			a.appendGuidance(&result, trMsg)
		}
		if cascadeGuidance := a.fixCascadeCheckCommand(tc.Name, tc.Arguments, result.IsError); cascadeGuidance != "" {
			// #952: explicit firing record (the old content scan could never
			// match - this detector's guidance header is "[HYPOTHESIS LOCK-IN
			// WARNING]", not the stale "[Fix Cascade" tag).
			a.crossDetectorConsensus.recordFiring("Fix Cascade", iteration+1)
			a.appendGuidance(&result, cascadeGuidance)
		}
		// Post-edit verification hint: after successful source-code edits,
		// periodically suggest running the build command to verify changes.
		if !result.IsError {
			if verifyHint := a.postEditVerifyHint(tc.Name, tc.Arguments); verifyHint != "" {
				a.appendGuidance(&result, verifyHint)
			}
		}
		// Convergence lock: detect post-verification unnecessary edits.
		// Fires when the agent continues editing after its changes verified.
		if convergenceGuidance := a.convergenceCheck(); convergenceGuidance != "" {
			a.crossDetectorConsensus.recordFiring("Convergence Lock", iteration+1)
			a.appendGuidance(&result, convergenceGuidance)
		}
		// Diminishing edit: detect polish-spiral (progressively smaller edits).
		if diminishingGuidance := a.diminishingCheck(); diminishingGuidance != "" {
			a.appendGuidance(&result, diminishingGuidance)
		}
		// Premature refactoring: detect unverified code restructuring.
		if refactorGuidance := a.prematureRefactorCheck(); refactorGuidance != "" {
			a.appendGuidance(&result, refactorGuidance)
		}
		// Cross-detector consensus: check for co-occurrence of detector
		// firings. #952: every detector in the chain above now records its
		// firing EXPLICITLY via recordFiring when it returns guidance, so no
		// content scanning is needed here. The old baseline-offset scan
		// (#147) both missed every detector appended before the scan window
		// (failureMode, errorCascade, ...) and risked false positives on raw
		// tool output containing tag literals.
		if consensusGuidance := a.crossDetectorConsensus.checkOnly(); consensusGuidance != "" {
			a.appendGuidance(&result, consensusGuidance)
		}
		// Collect follow-up messages from tools (e.g., inline skills).
		if len(result.FollowUpMessages) > 0 {
			followUpMessages = append(followUpMessages, result.FollowUpMessages...)
		}
		// If the tool suggests a working directory change, apply it.
		if result.SuggestedWorkingDir != "" && !result.IsError {
			a.mu.Lock()
			oldDir := a.workingDir
			a.workingDir = result.SuggestedWorkingDir
			a.mu.Unlock()
			debug.Log("agent", "working dir changed: %s -> %s (suggested by %s)", oldDir, result.SuggestedWorkingDir, tc.Name)
		}
		// Prompt injection guard: scan external-content tool results for
		// adversarial injection patterns and wrap them with a security
		// notice so the model treats them as untrusted data.
		result.Content = guardPromptInjection(tc.Name, tc.Arguments, result.Content)
		// Tainted data influence tracking (IFC): when the injection guard
		// flags tool output, record distinctive fingerprints so we can
		// later detect if that tainted content flows into privileged
		// tool calls (edit_file, write_file, run_command, etc.).
		// Research: Microsoft IFC (arXiv:2505.23643), OWASP ATR-2026-00032.
		a.taintInfluence.recordIfTainted(tc.Name, result.Content)
		// Spiral-of-hallucination: an execution-type tool call with
		// observable side effects breaks the spiral chain (#161 — prose
		// keyword matching fired on nearly every turn; #167 — read-only
		// tools must not count as verification).
		if !result.IsError {
			a.recordSpiralVerification(tc.Name)
		}
		// Tool-overuse write bookkeeping is POST-execution (#495): only
		// a successful edit/write makes later reads suspicious. The old
		// pre-execution recordWrite counted failed edits too, so the
		// recovery read_file after a failed edit received false-premise
		// "trust the content from your edit" guidance that contradicts
		// edit_fail_recovery's own recommendation.
		// Repetitive-line compression: collapse consecutive identical or
		// template-similar lines (common in build/test/install output) before
		// the size-based guard. This may prevent truncation entirely for
		// outputs that are large only due to repetition.
		if compressed := compressRepetitiveLines(result.Content); len(compressed) < len(result.Content) {
			debug.Log("compress", "repetitive-line compression: tool=%s %d→%d bytes", tc.Name, len(result.Content), len(compressed))
			result.Content = compressed
		}
		// Context-fill-aware output guard: proactively truncate large
		// non-error results when context is getting full. This prevents
		// a single 50KB build log from consuming 12K+ tokens when the
		// context window is already under pressure. Head-tail preservation
		// ensures the agent sees both context (head) and errors/results (tail).
		if !result.IsError {
			threshold := a.contextManager.AutoCompactThreshold()
			if threshold > 0 {
				fillRatio := float64(a.contextManager.TokenCount()) / float64(threshold)
				if truncated := guardToolOutput(result.Content, fillRatio); len(truncated) < len(result.Content) {
					debug.Log("agent", "tool output guarded: tool=%s tokens=%d threshold=%d fill=%.0f%% %d→%d bytes", tc.Name, a.contextManager.TokenCount(), threshold, fillRatio*100, len(result.Content), len(truncated))
					guarded := truncated
					// Tool Output Offloading: persist the FULL original
					// output to disk so the discarded middle section is
					// recoverable via read_file/grep on the spill path
					// instead of being lost forever (LangChain harness
					// anatomy, 2026). Best-effort: on failure fall back
					// to plain truncation.
					if spillPath := a.outputOffload.spill(tc.Name, result.Content); spillPath != "" {
						guarded += spillNotice(spillPath, len(result.Content))
						debug.Log("agent", "tool output offloaded: tool=%s path=%s originalLen=%d", tc.Name, spillPath, len(result.Content))
					}
					result.Content = withTruncationAdvisory(guarded, tc.Name, len(result.Content))
					a.truncClaim.recordTruncation(tc.Name, iteration)
					// Back-fill the chain. The (post-advisory) content is passed so a
					// truncation that carries its own recovery advisory is skipped -
					// same exemption classifyDegraded applies to designed paging
					// footers.
					a.errorPropagate.recordGuardedTruncation(tc.Name, result.Content)
				}
			}
		}
		// Apply coalesced guidance hints to the tool result content.
		// Both vision and non-vision paths share the same hint assembly logic.
		// originalContentLen was captured BEFORE the detector chain above (#952,
		// #553 residual) so detector guidance never inflates waste metering.
		// #1819 case 2: capture the POST-shrink length here — compress/
		// guardToolOutput may have rewritten Content between the original
		// capture and this point, and metering must reflect what actually
		// enters the context (the #952 "real context cost" intent cuts both
		// ways: a 60KB→6KB compressed error log metered at 60KB inflates the
		// waste ratio past the 40% threshold early; a truncated large read
		// metered at full size dilutes it).
		measuredLen := len(result.Content)
		a.applyToolResultGuidance(&result, loopGuidance, searchParamHint, redundancyHint, equivHint, undoBlindHint)
		// Untrusted-content spotlighting: the block sent to the provider
		// is wrapped in untrusted_tool_output markers (data marking);
		// TUI events and detector chains keep the raw result.Content.
		llmContent := spotlightUntrustedOutput(tc.Name, result.Content)
		if len(result.Images) > 0 && a.SupportsVision() {
			imgs := make([]provider.ContentImage, len(result.Images))
			for imgIdx, ri := range result.Images {
				imgs[imgIdx] = provider.ContentImage{MIME: ri.MIME, Base64: ri.Base64}
			}
			toolResults = append(toolResults, provider.ToolResultWithImages(tc.ID, tc.Name, llmContent, imgs, result.IsError))
		} else {
			toolResults = append(toolResults, provider.ToolResultNamedBlock(tc.ID, tc.Name, llmContent, result.IsError))
		}
		onEvent(provider.StreamEvent{
			Type:    provider.StreamEventToolResult,
			Tool:    tc,
			Result:  result.Content,
			IsError: result.IsError,
		})
		// Register read-only tool results for in-turn deduplication.
		if speculativeSafeTools[tc.Name] && !result.IsError {
			seenReadOnly[dedupK] = len(toolResults) - 1
		}
		// Token waste budget tracking (AgentDiet arXiv:2509.23586):
		// record each tool result's estimated token cost and waste category.
		var pathsRead []string
		if tc.Name == "read_file" || tc.Name == "multi_file_read" {
			pathsRead = extractReadFilePaths(tc.Name, tc.Arguments)
		}
		isRedundant := redundancyHint != ""
		if a.tokenWasteBudget != nil {
			// #1819 case 2: measuredLen (post-compress/guard, pre-hint) is the
			// real context cost; originalContentLen stays available upstream for
			// anything that needs the pre-shrink size.
			a.tokenWasteBudget.recordToolResultLen(tc.Name, result.Content, measuredLen, result.IsError, isRedundant, pathsRead)
		}
		if err := ctx.Err(); err != nil {
			// Context cancelled after completing some tools. Fill "cancelled"
			// results for remaining tool_calls that have not run yet.
			a.fillCancelledToolResults(toolCalls[idx+1:], &toolResults)
			// fillCancelledToolResults adds to contextManager only when
			// pending > 0. If this was the last tool call, we still need to
			// add the completed results to keep tool_use/tool_result pairs
			// balanced for the next LLM call.
			if len(toolCalls[idx+1:]) == 0 && len(toolResults) > 0 {
				a.contextManager.Add(provider.Message{
					Role:    "user",
					Content: toolResults,
				})
			}
			return toolBatchOutcome{cancelErr: err}
		}
	}
	return toolBatchOutcome{
		toolResults:           toolResults,
		followUpMessages:      followUpMessages,
		deferredMemoryContent: deferredMemoryContent,
		deferredMemoryFiles:   deferredMemoryFiles,
		deferredMemoryTarget:  deferredMemoryTarget,
	}
}
