package agent

import (
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

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

func TestIssue2808BatchReplaceBumpsEpoch(t *testing.T) {
	l := newToolDedupLedger()
	l.ttl = 5 * time.Second

	l.record("run_command", `{"command":"go test ./pkg/"}`, tool.Result{Content: "PASS"})
	before := l.epoch

	l.record("batch_replace", `{"pattern":"x","replacement":"y","files":["a.go"]}`, tool.Result{Content: "2 files changed"})
	if l.epoch != before+1 {
		t.Fatalf("batch_replace success did not bump epoch: got %d, want %d", l.epoch, before+1)
	}

	if got := l.suppressDuplicate("run_command", `{"command":"go test ./pkg/"}`); got != nil {
		t.Fatalf("identical verify command suppressed after batch_replace edit (stale replay): %q", got.Content)
	}
}

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

func TestIssue2808UndoEditBumpsEpochWithoutSuppression(t *testing.T) {
	l := newToolDedupLedger()
	l.ttl = 5 * time.Second

	l.record("run_command", `{"command":"go test ./pkg/"}`, tool.Result{Content: "PASS"})
	before := l.epoch

	l.record("undo_edit", `{"action":"undo"}`, tool.Result{Content: "reverted"})
	if l.epoch != before+1 {
		t.Fatalf("undo_edit success did not bump epoch: got %d, want %d", l.epoch, before+1)
	}

	if got := l.suppressDuplicate("run_command", `{"command":"go test ./pkg/"}`); got != nil {
		t.Fatalf("identical verify command suppressed after undo_edit (stale replay): %q", got.Content)
	}

	if got := l.suppressDuplicate("undo_edit", `{"action":"undo"}`); got != nil {
		t.Fatalf("undo_edit self-suppressed (undo is not idempotent; second undo must execute): %q", got.Content)
	}

	l2 := newToolDedupLedger()
	l2.record("undo_edit", `{"action":"undo"}`, tool.Result{Content: "nothing to undo", IsError: true})
	if l2.epoch != 0 {
		t.Fatalf("failed undo_edit bumped epoch: got %d, want 0", l2.epoch)
	}
}

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
