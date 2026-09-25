package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestIssue2766MultiEditFileEpochBump pins the #2766 fix: a successful
// multi_edit_file must bump the workspace epoch so that re-running the SAME
// test command after a batch edit is not suppressed as a duplicate (the
// verify loop must stay live). Before the fix, multi_edit_file was listed in
// fileMutatingTools but missing from mutatingToolNames, so record()'s
// isMutatingTool gate early-returned and the epoch never bumped.
func TestIssue2766MultiEditFileEpochBump(t *testing.T) {
	l := newToolDedupLedger()

	// 1. A successful test run is recorded (IsError=false so it caches).
	testArgs := `{"command":"go test ./internal/agent/"}`
	l.record("run_command", testArgs, tool.Result{Content: "FAIL\n--- FAIL: TestX"})

	// 2. A successful multi_edit_file fix must bump the epoch.
	before := l.epoch
	l.record("multi_edit_file", `{"edits":[{"old_text":"a","new_text":"b"}]}`, tool.Result{Content: "ok"})
	if l.epoch != before+1 {
		t.Fatalf("multi_edit_file success did not bump epoch: got %d, want %d", l.epoch, before+1)
	}

	// 3. The identical test command must NOT be suppressed after the edit:
	// the epoch term in the fingerprint changed, so the stale entry misses.
	if got := l.suppressDuplicate("run_command", testArgs); got != nil {
		t.Fatalf("identical run_command was suppressed after multi_edit_file fix (stale FAIL replay): %q", got.Content)
	}
}

// TestIssue2766FileMutatingToolsSubsetOfMutating guards the class of bug:
// every entry in fileMutatingTools must also pass the isMutatingTool gate,
// otherwise its epoch-bump branch in record() is unreachable dead code.
func TestIssue2766FileMutatingToolsSubsetOfMutating(t *testing.T) {
	for name := range fileMutatingTools {
		if !isMutatingTool(name) {
			t.Errorf("fileMutatingTools[%q] is not a mutating tool: record() early-returns before the epoch bump, making the entry dead code", name)
		}
	}
}

// TestIssue2766SuppressionStillWorksForTrueDuplicates ensures the fix did not
// break the dedup's original purpose: an identical mutating call with no
// interleaved workspace change is still suppressed (replayed, not re-executed).
func TestIssue2766SuppressionStillWorksForTrueDuplicates(t *testing.T) {
	l := newToolDedupLedger()
	args := `{"command":"gh pr create --title x"}`
	l.record("run_command", args, tool.Result{Content: "https://example/pr/1"})

	got := l.suppressDuplicate("run_command", args)
	if got == nil {
		t.Fatal("true duplicate mutating call was not suppressed")
	}
	if got.IsError {
		t.Error("suppressed replay must not be flagged as error")
	}
	if want := "[suppressed-as-duplicate]"; !strings.Contains(got.Content, want) {
		t.Errorf("replay missing advisory header: %q", got.Content)
	}
}
