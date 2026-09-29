package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestIssue2808CanonicalMutatingToolsInDedupTables pins the #2808 fix:
// every file-mutating tool in the canonical sourceMutatingTools superset
// must be present in BOTH tool_dedup gates, otherwise its re-execution is
// never suppressed and (worse) its success does not bump the workspace
// epoch, so a post-edit re-run of the same verify command replays stale
// results (#2486 danger direction).
func TestIssue2808CanonicalMutatingToolsInDedupTables(t *testing.T) {
	for name := range sourceMutatingTools {
		if !mutatingToolNames[name] {
			t.Errorf("canonical mutating tool %q missing from mutatingToolNames (no dedup suppression)", name)
		}
		if !fileMutatingTools[name] {
			t.Errorf("canonical mutating tool %q missing from fileMutatingTools (no epoch bump after edit)", name)
		}
	}
}

// TestIssue2808BatchReplaceBumpsEpoch pins the concrete danger path:
// batch_replace success must bump the epoch so a re-run of an identical
// verify command after the bulk edit is NOT suppressed.
func TestIssue2808BatchReplaceBumpsEpoch(t *testing.T) {
	l := newToolDedupLedger()
	l.ttl = 5 * time.Second

	// Step 1: successful verify command recorded at epoch E.
	l.record("run_command", `{"command":"go test ./pkg/"}`, tool.Result{Content: "PASS"})
	before := l.epoch

	// Step 2: batch_replace succeeds after the verify — must bump epoch.
	l.record("batch_replace", `{"pattern":"x","replacement":"y","files":["a.go"]}`, tool.Result{Content: "2 files changed"})
	if l.epoch != before+1 {
		t.Fatalf("batch_replace success did not bump epoch: got %d, want %d", l.epoch, before+1)
	}

	// Step 3: identical verify command must NOT be suppressed anymore.
	if got := l.suppressDuplicate("run_command", `{"command":"go test ./pkg/"}`); got != nil {
		t.Fatalf("identical verify command suppressed after batch_replace edit (stale replay): %q", got.Content)
	}
}

// TestIssue2808LspRenameAndMultiFileEditBumpEpoch covers the other two
// escape tools from #2808.
func TestIssue2808LspRenameAndMultiFileEditBumpEpoch(t *testing.T) {
	for _, name := range []string{"lsp_rename", "multi_file_edit", "multi_edit_file"} {
		l := newToolDedupLedger()
		l.ttl = 5 * time.Second
		before := l.epoch
		l.record(name, `{"x":"y"}`, tool.Result{Content: "ok"})
		if l.epoch != before+1 {
			t.Errorf("%s success did not bump epoch: got %d, want %d", name, l.epoch, before+1)
		}
	}
}

// TestIssue2808UndoEditBumpsEpochWithoutSuppression pins the undo_edit gap
// from the #2808 issue comment (independent re-review sa-166): a successful
// undo is a real disk write (#1104) and must bump the epoch so a re-run of
// the same verify command after the undo is NOT suppressed — but undo is not
// idempotent (each call reverts one more checkpoint), so identical repeat
// undo_edit calls must never be suppressed-and-replayed either.
func TestIssue2808UndoEditBumpsEpochWithoutSuppression(t *testing.T) {
	l := newToolDedupLedger()
	l.ttl = 5 * time.Second

	// Step 1: successful verify command recorded at epoch E.
	l.record("run_command", `{"command":"go test ./pkg/"}`, tool.Result{Content: "PASS"})
	before := l.epoch

	// Step 2: undo_edit succeeds — must bump epoch, not enter the table.
	l.record("undo_edit", `{"action":"undo"}`, tool.Result{Content: "reverted"})
	if l.epoch != before+1 {
		t.Fatalf("undo_edit success did not bump epoch: got %d, want %d", l.epoch, before+1)
	}

	// Step 3: identical verify command must NOT be suppressed anymore.
	if got := l.suppressDuplicate("run_command", `{"command":"go test ./pkg/"}`); got != nil {
		t.Fatalf("identical verify command suppressed after undo_edit (stale replay): %q", got.Content)
	}

	// Step 4: undo_edit itself must never be suppressed-and-replayed — the
	// second undo reverts a DIFFERENT checkpoint and must really execute.
	if got := l.suppressDuplicate("undo_edit", `{"action":"undo"}`); got != nil {
		t.Fatalf("undo_edit self-suppressed (undo is not idempotent; second undo must execute): %q", got.Content)
	}

	// Step 5: failed undo must not bump the epoch.
	l2 := newToolDedupLedger()
	l2.record("undo_edit", `{"action":"undo"}`, tool.Result{Content: "nothing to undo", IsError: true})
	if l2.epoch != 0 {
		t.Fatalf("failed undo_edit bumped epoch: got %d, want 0", l2.epoch)
	}
}

// TestIssue2808BatchReplaceParityWithEditFile pins the protection-side
// semantics: fileMutatingTools members bump the epoch on their own record,
// so an identical call is deliberately NOT self-suppressed (same safe
// direction as edit_file - false suppression is the dangerous direction
// per #2486). batch_replace must share that contract exactly.
func TestIssue2808BatchReplaceParityWithEditFile(t *testing.T) {
	for _, name := range []string{"batch_replace", "edit_file", "lsp_rename", "multi_file_edit"} {
		l := newToolDedupLedger()
		l.ttl = 5 * time.Second
		args := `{"x":"y"}`
		l.record(name, args, tool.Result{Content: "ok"})
		if got := l.suppressDuplicate(name, args); got != nil {
			t.Errorf("%s: identical call self-suppressed (contradicts fileMutatingTools epoch-bump contract): %q", name, got.Content)
		}
	}
}
