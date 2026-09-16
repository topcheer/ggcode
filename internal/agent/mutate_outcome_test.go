package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestClassifyMutatingFailure(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		content  string
		expected mutatingOutcomeClass
	}{
		{"timeout", "run_command", "tool error: command timed out after 1800s", outcomeAmbiguous},
		{"deadline", "git_commit", "tool error: context deadline exceeded", outcomeAmbiguous},
		{"cancel-race", "run_command", "tool run_command was cancelled (it did not respond to cancellation...)", outcomeAmbiguous},
		{"panic", "git_stash", "tool git_stash panicked: nil map access", outcomeAmbiguous},
		{"preexec-param", "write_file", "write_file: missing required parameter 'path'", outcomePreExec},
		{"preexec-declined", "multi_file_edit", "Multi-file write cancelled by user.", outcomePreExec},
		{"preexec-sandbox", "file_ops", "path blocked by sandbox policy", outcomePreExec},
		{"shell-exit-no-class", "run_command", "exit status 1: compile error", outcomeNone},
		{"shell-never-preexec", "run_command", "git: permission denied", outcomeNone},
		{"read-only-ignored", "grep", "timeout", outcomeNone},
		{"empty", "git_commit", "", outcomeNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyMutatingFailure(tc.tool, tc.content)
			if got != tc.expected {
				t.Fatalf("classifyMutatingFailure(%q, %q) = %v, want %v", tc.tool, tc.content, got, tc.expected)
			}
		})
	}
}

func TestHasNonAtomicSemantics(t *testing.T) {
	for _, name := range []string{"run_command", "git_commit", "write_file", "edit_file", "multi_file_edit", "file_ops", "undo_edit"} {
		if !hasNonAtomicSemantics(name) {
			t.Errorf("%s should have non-atomic semantics", name)
		}
	}
	for _, name := range []string{"grep", "read_file", "web_search", "lsp_diagnostics"} {
		if hasNonAtomicSemantics(name) {
			t.Errorf("%s should NOT have non-atomic semantics", name)
		}
	}
}

func TestMutatingLedgerRecordAndLookup(t *testing.T) {
	l := newMutatingLedger()
	if got := l.lookupAmbiguous("run_command", []byte(`{"command":"x"}`)); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	l.recordAmbiguous("run_command", []byte(`{"command":"x"}`))
	l.recordAmbiguous("run_command", []byte(`{"command":"x"}`))
	if got := l.lookupAmbiguous("run_command", []byte(`{"command":"x"}`)); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	// Different args = different key
	if got := l.lookupAmbiguous("run_command", []byte(`{"command":"y"}`)); got != 0 {
		t.Fatalf("expected 0 for other args, got %d", got)
	}
	l.reset()
	if got := l.lookupAmbiguous("run_command", []byte(`{"command":"x"}`)); got != 0 {
		t.Fatalf("expected reset to clear, got %d", got)
	}
}

func TestMutatingLedgerEviction(t *testing.T) {
	l := newMutatingLedger()
	for i := 0; i < maxMutatingLedgerEntries+10; i++ {
		l.recordAmbiguous("run_command", []byte{byte(i)})
	}
	l.mu.Lock()
	size := len(l.entries)
	l.mu.Unlock()
	if size != maxMutatingLedgerEntries {
		t.Fatalf("ledger not bounded: %d entries", size)
	}
}

func TestAnnotateMutatingOutcomeAmbiguous(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	args := []byte(`{"command":"git commit"}`)
	res := tool.Result{Content: "tool error: command timed out", IsError: true}
	out := a.annotateMutatingOutcome("git_commit", args, res, 0)
	if !strings.Contains(out.Content, nonAtomicResultTag) {
		t.Fatalf("missing tag:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "UNKNOWN") || !strings.Contains(out.Content, "Do NOT blindly re-issue") {
		t.Fatalf("missing ambiguous guidance:\n%s", out.Content)
	}
	if got := a.mutateLedger.lookupAmbiguous("git_commit", args); got != 1 {
		t.Fatalf("ledger not updated: %d", got)
	}
}

func TestAnnotateMutatingOutcomePreExec(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	res := tool.Result{Content: "write_file: missing required parameter 'content'", IsError: true}
	out := a.annotateMutatingOutcome("write_file", []byte("{}"), res, 0)
	if !strings.Contains(out.Content, "[no-side-effect]") {
		t.Fatalf("missing pre-exec note:\n%s", out.Content)
	}
	if strings.Contains(out.Content, nonAtomicResultTag) {
		t.Fatalf("pre-exec failure must not carry ambiguous tag:\n%s", out.Content)
	}
	// No ledger entry recorded for pre-exec
	if got := a.mutateLedger.lookupAmbiguous("write_file", []byte("{}")); got != 0 {
		t.Fatalf("pre-exec must not record ambiguous: %d", got)
	}
}

func TestAnnotateMutatingOutcomeDoubleApplyOnRetrySuccess(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	args := []byte(`{"command":"git commit -m x"}`)
	res := tool.Result{Content: "committed abc123", IsError: false}
	out := a.annotateMutatingOutcome("git_commit", args, res, 1)
	if !strings.Contains(out.Content, "duplicate side effects") {
		t.Fatalf("missing double-apply note:\n%s", out.Content)
	}
}

func TestAnnotateMutatingOutcomeSuccessClean(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	res := tool.Result{Content: "ok", IsError: false}
	out := a.annotateMutatingOutcome("git_commit", []byte("{}"), res, 0)
	if out.Content != "ok" {
		t.Fatalf("clean success must be untouched, got:\n%s", out.Content)
	}
}

func TestAnnotateMutatingOutcomeReadOnlyUntouched(t *testing.T) {
	a := &Agent{mutateLedger: newMutatingLedger()}
	a.guidanceBudget.reset()
	res := tool.Result{Content: "tool error: timeout", IsError: true}
	out := a.annotateMutatingOutcome("grep", []byte("{}"), res, 0)
	if out.Content != res.Content {
		t.Fatalf("read-only tool must not be annotated:\n%s", out.Content)
	}
}

func TestNonAtomicTagIsCritical(t *testing.T) {
	if !criticalHintTags["non-atomic"] {
		t.Fatal("[non-atomic] must be registered in criticalHintTags to bypass the per-turn guidance cap")
	}
}

// TestMutatingLedgerNilSafe: Agent literals in tests (&Agent{}) leave
// mutateLedger nil; the executeToolCall wiring must not panic on it.
func TestMutatingLedgerNilSafe(t *testing.T) {
	var l *mutatingLedger
	if got := l.lookupAmbiguous("git_commit", []byte("{}")); got != 0 {
		t.Fatalf("nil lookup should be 0, got %d", got)
	}
	l.recordAmbiguous("git_commit", []byte("{}")) // must not panic
	l.reset()                                     // must not panic
	// Full nil-Agent path through annotateMutatingOutcome.
	a := &Agent{}
	a.guidanceBudget.reset()
	res := tool.Result{Content: "tool error: timed out", IsError: true}
	out := a.annotateMutatingOutcome("git_commit", []byte("{}"), res, 0)
	if !strings.Contains(out.Content, nonAtomicResultTag) {
		t.Fatalf("nil-ledger agent must still annotate:\n%s", out.Content)
	}
}
