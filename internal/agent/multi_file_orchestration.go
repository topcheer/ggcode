package agent

// multi_file_orchestration.go holds the per-phase helpers extracted from
// executeMultiFileTool (r108 code-health project: complexity 49 -> flat
// orchestrator). Every fix annotation (#1786, #2143, #2138, #1864, #601 W2,
// #1035) is migrated verbatim; phase order and the appendGuidance
// sequencing are part of the guidance_budget contract and must not be
// reordered.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/diff"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/tool"
)

// confirmMultiFileDiff shows the preview diff for user confirmation.
// It reports whether the user CANCELLED the batch (diffFn returned false).
// Batches with no real content changes skip confirmation entirely.
func confirmMultiFileDiff(ctx context.Context, diffFn DiffConfirmFunc, plans []tool.PlannedFileEdit) bool {
	if diffText, hasChanges := buildMultiFileDiffText(plans); hasChanges {
		label := fmt.Sprintf("%d files", len(plans))
		if len(plans) == 1 {
			label = plans[0].Path
		}
		return !diffFn(ctx, label, diffText)
	}
	return false
}

// buildMultiFileEditPlans keeps only plans that carry real content changes,
// mirroring what the executor will actually write.
func buildMultiFileEditPlans(plans []tool.PlannedFileEdit) []fileEditPlan {
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
	return planBatch
}

// multiFilePreWriteBlock runs the batch dry-run validation and formats the
// block result. blocked=true means NO files may be written.
func multiFilePreWriteBlock(plans []tool.PlannedFileEdit) (result tool.Result, blocked bool) {
	blockers := dryRunValidateBatch(buildMultiFileEditPlans(plans))
	if len(blockers) == 0 {
		return tool.Result{}, false
	}
	var b strings.Builder
	b.WriteString("[Multi-file edit blocked by pre-write validation]\n")
	b.WriteString("One or more files have fatal issues. NO files were modified.\n\n")
	for path, msg := range blockers {
		b.WriteString(fmt.Sprintf("File: %s\n%s\n\n", path, msg))
	}
	return tool.Result{Content: strings.TrimRight(b.String(), "\n"), IsError: true}, true
}

// refreshMultiFilePlanBaselines re-reads every planned file and refreshes a
// stale plan baseline in place.
//
// #1786 case 1 (multi-file leg): same TOCTOU as the single-file path -
// plan.OldContent comes from the PreviewChanges first read, and the
// diffConfirm pause above opens an arbitrary window for external
// writers. Refresh each stale plan baseline so per-file undo restores
// the true pre-write state instead of erasing external changes.
func refreshMultiFilePlanBaselines(plans []tool.PlannedFileEdit) {
	for i := range plans {
		cur, rerr := os.ReadFile(plans[i].Path)
		if rerr != nil {
			continue // unreadable now = executor will surface its own error
		}
		if string(cur) != plans[i].OldContent {
			debug.Log("agent", "#1786 baseline drift on %s: refreshing plan baseline", plans[i].Path)
			plans[i].OldContent = string(cur)
		}
	}
}

// saveMultiFileCheckpoints persists per-file undo entries for the paths the
// tool result reports as written. cpMgr may be nil (checkpoints disabled)
// and the result content may not be a MultiFileEditContent JSON at all -
// both cases are silent no-ops, exactly as before the extraction.
func saveMultiFileCheckpoints(cpMgr *checkpoint.Manager, plans []tool.PlannedFileEdit, resultContent, toolName string) {
	if cpMgr == nil || len(plans) == 0 {
		return
	}
	var outcome tool.MultiFileEditContent
	if err := json.Unmarshal([]byte(resultContent), &outcome); err != nil {
		return
	}
	planByPath := make(map[string]tool.PlannedFileEdit, len(plans))
	for _, plan := range plans {
		planByPath[plan.Path] = plan
	}
	for _, path := range outcome.WrittenPaths {
		if plan, ok := planByPath[path]; ok {
			cpMgr.Save(path, plan.OldContent, plan.NewContent, toolName)
		}
	}
}

// probeMultiFileOutcome parses a multi-file tool result for the two probe
// fields the post-write checks need.
//
// #2143 P1: a dry-run preview (batch_replace dry_run=true) writes
// NOTHING - running the read-back comparison against unwritten plans
// flagged every plan as a "post-write mismatch" with a wrong semantic
// ("write may be partial") on EVERY preview.
//
// #2143 P2: partial_success mode reports per-file outcomes -
// written_paths is authoritative (a pointer probe distinguishes "tool
// reports no such field" from "field present but empty", i.e. all
// files failed). Unwritten files keep their old disk content and must
// not trip the mismatch check.
//
// writtenSet is nil when the tool did not report written_paths at all
// (a non-nil set - even empty - means the field was present).
func probeMultiFileOutcome(content string) (isDryRun bool, writtenSet map[string]bool) {
	var dryProbe struct {
		DryRun bool `json:"dry_run"`
	}
	isDryRun = json.Unmarshal([]byte(content), &dryProbe) == nil && dryProbe.DryRun
	var wpProbe struct {
		WrittenPaths *[]string `json:"written_paths"`
	}
	if json.Unmarshal([]byte(content), &wpProbe) == nil && wpProbe.WrittenPaths != nil {
		writtenSet = make(map[string]bool, len(*wpProbe.WrittenPaths))
		for _, p := range *wpProbe.WrittenPaths {
			writtenSet[p] = true
		}
	}
	return isDryRun, writtenSet
}

// collectMultiFileIntegrityWarnings validates each actually-written file
// against what was planned, returning at most one warning per plan in plan
// order.
func collectMultiFileIntegrityWarnings(plans []tool.PlannedFileEdit, writtenSet map[string]bool) []string {
	var warnings []string
	for _, plan := range plans {
		if writtenSet != nil && !writtenSet[plan.Path] {
			continue // not actually written (failed/skipped in partial mode)
		}
		if diff.HasChanges(plan.OldContent, plan.NewContent) {
			// #2138: the multi-file tools (multi_file_write/edit,
			// multi_edit_file, batch_replace) persist gofmt-FORMATTED bytes
			// for .go files unconditionally - passing the raw plan.NewContent
			// here made every gofmt-touched write report a fake post-write
			// mismatch (#2132 fixed only the single-file leg). Mirror the
			// write-time formatting so mismatch means REAL drift here too.
			mirrored := mirrorWriteTimeGoFormat(plan.Path, plan.NewContent)
			if w := checkWriteIntegrity(plan.Path, plan.OldContent, mirrored); w != "" {
				warnings = append(warnings, w)
			}
		}
	}
	return warnings
}

// collectMultiFileTestCompanionWarnings flags source edits that lack their
// test companion, in plan order.
func collectMultiFileTestCompanionWarnings(plans []tool.PlannedFileEdit) []string {
	var warnings []string
	for _, plan := range plans {
		if diff.HasChanges(plan.OldContent, plan.NewContent) {
			if w := CheckMissingTestCompanionWithFS(plan.Path, plan.OldContent, plan.NewContent); w != "" {
				warnings = append(warnings, w)
			}
		}
	}
	return warnings
}

// collectMultiFileDebugWarnings flags newly added debug statements, in plan
// order.
//
// Post-write hardcoded credential detection for multi-file edits
// (#601 W2): the per-plan checkWriteIntegrity loop above already runs the
// registry's "hardcoded-secret" check for each file; the direct duplicate
// call below was removed so each secret surfaces exactly once (the
// registry copy respects the maxIntegrityWarnings cap).
func collectMultiFileDebugWarnings(plans []tool.PlannedFileEdit) []string {
	var warnings []string
	for _, plan := range plans {
		if diff.HasChanges(plan.OldContent, plan.NewContent) {
			if w := checkDebugStmts(plan.Path, plan.OldContent, plan.NewContent); w != "" {
				warnings = append(warnings, w)
			}
		}
	}
	return warnings
}

// appendMultiFilePostWriteGuidance routes the three post-write warning
// families (integrity, test companion, debug statements) through the shared
// guidance budget. The family order below is load-bearing: integrity first,
// then test companion, then debug statements, each in plan order.
func (a *Agent) appendMultiFilePostWriteGuidance(result *tool.Result, plans []tool.PlannedFileEdit) {
	if result.IsError || len(plans) == 0 {
		return
	}
	isDryRun, writtenSet := probeMultiFileOutcome(result.Content)
	if !isDryRun {
		for _, w := range collectMultiFileIntegrityWarnings(plans, writtenSet) {
			// #1864 case 2: route through appendGuidance so the shared per-turn
			// budget applies - the direct += appends let N plans stack 3N warning
			// blocks that neither charged the count cap nor the 2048-byte pool,
			// breaking guidance_budget's "all paths share one pool" contract.
			a.appendGuidance(result, w)
		}
	}

	for _, w := range collectMultiFileTestCompanionWarnings(plans) {
		a.appendGuidance(result, w) // #1864 case 2: budgeted path
	}

	for _, w := range collectMultiFileDebugWarnings(plans) {
		a.appendGuidance(result, w) // #1864 case 2: budgeted path
	}
}

// runMultiFilePostHooks runs the PostToolUse hooks with the multi-file
// execution metadata and appends any hook output to the result.
func runMultiFilePostHooks(result *tool.Result, hookCfg hooks.HookConfig, env hooks.HookEnv, dur time.Duration) {
	postEnv := env
	postEnv.ToolSuccess = !result.IsError
	if result.IsError {
		postEnv.ToolError = truncateString(result.Content, 500)
	}
	postEnv.ToolResult = truncateString(result.Content, 4096)
	postEnv.ToolDuration = dur.String()
	postResult := hooks.RunPostHooks(hookCfg.PostToolUse, postEnv)
	if postResult.Output != "" {
		result.Content += "\n" + postResult.Output
	}
}
