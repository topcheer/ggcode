package agent

// Tests for the executeMultiFileTool phase helpers extracted in
// multi_file_orchestration.go (r108 code-health project). Each test pins
// one phase's behavior so future refactors cannot silently reorder or
// change semantics (#1786, #2143, #1864 contracts).

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/tool"
)

func TestConfirmMultiFileDiff(t *testing.T) {
	plans := []tool.PlannedFileEdit{
		{Path: "a.txt", OldContent: "old", NewContent: "new"},
	}
	cancel := func(ctx context.Context, label, diffText string) bool { return false }
	approve := func(ctx context.Context, label, diffText string) bool { return true }

	if !confirmMultiFileDiff(context.Background(), cancel, plans) {
		t.Error("diffFn=false must report cancellation")
	}
	if confirmMultiFileDiff(context.Background(), approve, plans) {
		t.Error("diffFn=true must not report cancellation")
	}

	// No real content changes -> no confirmation prompt at all.
	unchanged := []tool.PlannedFileEdit{{Path: "a.txt", OldContent: "same", NewContent: "same"}}
	called := false
	spy := func(ctx context.Context, label, diffText string) bool { called = true; return true }
	confirmMultiFileDiff(context.Background(), spy, unchanged)
	if called {
		t.Error("unchanged batch must not invoke diffFn")
	}

	// Single-file batch uses the file path as the label.
	var gotLabel string
	single := func(ctx context.Context, label, diffText string) bool { gotLabel = label; return true }
	confirmMultiFileDiff(context.Background(), single, plans)
	if gotLabel != "a.txt" {
		t.Errorf("single-file label = %q, want %q", gotLabel, "a.txt")
	}
}

func TestBuildMultiFileEditPlans(t *testing.T) {
	plans := []tool.PlannedFileEdit{
		{Path: "changed.txt", OldContent: "a", NewContent: "b"},
		{Path: "same.txt", OldContent: "x", NewContent: "x"},
	}
	batch := buildMultiFileEditPlans(plans)
	if len(batch) != 1 {
		t.Fatalf("expected 1 filtered plan, got %d", len(batch))
	}
	if batch[0].Path != "changed.txt" {
		t.Errorf("kept wrong plan: %+v", batch[0])
	}
	if batch[0].OldContent != "a" || batch[0].NewContent != "b" {
		t.Errorf("plan contents not carried over: %+v", batch[0])
	}
}

func TestMultiFilePreWriteBlock(t *testing.T) {
	// Valid batch -> not blocked.
	plans := []tool.PlannedFileEdit{
		{Path: "valid.go", OldContent: "package main\n", NewContent: "package main\n\nfunc main() {}\n"},
	}
	if _, blocked := multiFilePreWriteBlock(plans); blocked {
		t.Error("valid batch must not be blocked")
	}

	// Broken Go syntax -> blocked with a NO-files-modified error result.
	plans = append(plans, tool.PlannedFileEdit{
		Path: "broken.go", OldContent: "package main\n", NewContent: "package main\n\nfunc main() {\n",
	})
	result, blocked := multiFilePreWriteBlock(plans)
	if !blocked {
		t.Fatal("broken syntax must block the batch")
	}
	if !result.IsError {
		t.Error("blocked result must be an error")
	}
	if !strings.Contains(result.Content, "NO files were modified") {
		t.Errorf("blocked result missing no-modification notice: %q", result.Content)
	}
	if !strings.Contains(result.Content, "broken.go") {
		t.Errorf("blocked result must name the blocking file: %q", result.Content)
	}
}

func TestRefreshMultiFilePlanBaselines(t *testing.T) {
	dir := t.TempDir()
	drifted := filepath.Join(dir, "drifted.txt")
	external := filepath.Join(dir, "external.txt")
	if err := os.WriteFile(drifted, []byte("external edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	plans := []tool.PlannedFileEdit{
		{Path: drifted, OldContent: "original", NewContent: "planned new"},
		{Path: external, OldContent: "gone", NewContent: "planned new"}, // file missing on disk
	}
	refreshMultiFilePlanBaselines(plans)
	if plans[0].OldContent != "external edit" {
		t.Errorf("#1786: stale baseline not refreshed, got %q", plans[0].OldContent)
	}
	if plans[1].OldContent != "gone" {
		t.Errorf("unreadable path must keep its baseline, got %q", plans[1].OldContent)
	}

	// In-sync baseline stays untouched (no rewrite of identical content).
	if err := os.WriteFile(drifted, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	plans[0].OldContent = "stable"
	refreshMultiFilePlanBaselines(plans)
	if plans[0].OldContent != "stable" {
		t.Errorf("in-sync baseline changed: %q", plans[0].OldContent)
	}
}

func TestProbeMultiFileOutcome(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		isDryRun   bool
		wantSet    map[string]bool
		setPresent bool
	}{
		{"dry run flag", `{"dry_run":true}`, true, nil, false},
		{"written paths", `{"written_paths":["a.txt","b.txt"]}`, false, map[string]bool{"a.txt": true, "b.txt": true}, true},
		{"field present empty", `{"written_paths":[]}`, false, map[string]bool{}, true},
		{"field absent", `{}`, false, nil, false},
		{"both", `{"dry_run":false,"written_paths":["a.txt"]}`, false, map[string]bool{"a.txt": true}, true},
		{"non-json", "plain text output", false, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isDryRun, writtenSet := probeMultiFileOutcome(tc.content)
			if isDryRun != tc.isDryRun {
				t.Errorf("isDryRun = %v, want %v", isDryRun, tc.isDryRun)
			}
			if (writtenSet != nil) != tc.setPresent {
				t.Fatalf("writtenSet presence = %v, want %v (set=%v)", writtenSet != nil, tc.setPresent, writtenSet)
			}
			if tc.setPresent && !reflect.DeepEqual(writtenSet, tc.wantSet) {
				t.Errorf("writtenSet = %v, want %v", writtenSet, tc.wantSet)
			}
		})
	}
}

func TestCollectMultiFileIntegrityWarnings(t *testing.T) {
	dir := t.TempDir()
	drifted := filepath.Join(dir, "drifted.txt")
	ok := filepath.Join(dir, "ok.txt")
	if err := os.WriteFile(drifted, []byte("something else entirely"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ok, []byte("written as planned"), 0o644); err != nil {
		t.Fatal(err)
	}
	plans := []tool.PlannedFileEdit{
		{Path: drifted, OldContent: "orig", NewContent: "written as planned"},
		{Path: ok, OldContent: "orig", NewContent: "written as planned"},
		{Path: ok, OldContent: "same", NewContent: "same"}, // zero-delta plan skipped
	}

	// nil writtenSet = tool did not report per-file outcomes: all changed
	// plans are checked, only the drifted one warns.
	warnings := collectMultiFileIntegrityWarnings(plans, nil)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 integrity warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "post-write mismatch") {
		t.Errorf("warning text drifted: %q", warnings[0])
	}

	// Partial-success mode: paths not in writtenSet are skipped entirely.
	warnings = collectMultiFileIntegrityWarnings(plans, map[string]bool{ok: true})
	if len(warnings) != 0 {
		t.Errorf("plans outside writtenSet must be skipped, got %v", warnings)
	}

	// Empty-but-present writtenSet (all files failed): nothing is checked.
	warnings = collectMultiFileIntegrityWarnings(plans, map[string]bool{})
	if len(warnings) != 0 {
		t.Errorf("empty writtenSet must skip all plans, got %v", warnings)
	}
}

func TestCollectMultiFileWarningFamiliesSkipUnchanged(t *testing.T) {
	plans := []tool.PlannedFileEdit{{Path: "unchanged.txt", OldContent: "x", NewContent: "x"}}
	if got := collectMultiFileTestCompanionWarnings(plans); len(got) != 0 {
		t.Errorf("zero-delta plans must not trip test-companion check: %v", got)
	}
	if got := collectMultiFileDebugWarnings(plans); len(got) != 0 {
		t.Errorf("zero-delta plans must not trip debug-stmt check: %v", got)
	}
}

func TestRunMultiFilePostHooks(t *testing.T) {
	cfg := hooks.HookConfig{
		PostToolUse: []hooks.Hook{{
			Match:        "multi_file_edit",
			Command:      "echo multi-file-hook-ok",
			InjectOutput: true,
		}},
	}
	env := hooks.HookEnv{Event: hooks.EventPostToolUse, ToolName: "multi_file_edit", RawInput: `{}`}

	result := tool.Result{Content: "wrote 2 files"}
	runMultiFilePostHooks(&result, cfg, env, 0)
	if !strings.Contains(result.Content, "multi-file-hook-ok") {
		t.Errorf("hook output not appended, got %q", result.Content)
	}

	// Error results keep their truncation contract: ToolError populated,
	// ToolSuccess false, hook output still appended.
	errResult := tool.Result{Content: "write failed: disk full", IsError: true}
	runMultiFilePostHooks(&errResult, cfg, env, 0)
	if !strings.Contains(errResult.Content, "multi-file-hook-ok") {
		t.Errorf("hook output not appended on error result, got %q", errResult.Content)
	}
}
