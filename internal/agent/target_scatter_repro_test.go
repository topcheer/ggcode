package agent

import (
	"strings"
	"testing"
)

// --- #488: observational commands wiped the scatter window ---

// The issue's exact 8-step interleaved sequence: diagnostic tools mixed with
// ls/cat/pwd/git log must accumulate unique targets and eventually fire —
// previously every run_command cleared the window, so uniqueCount never
// passed the threshold of 5.
func TestTargetScatter_ObservationalCommandDoesNotClearWindow(t *testing.T) {
	s := newTargetScatterState()

	steps := []struct{ tool, args string }{
		{"read_file", `{"path":"/repo/internal/agent/agent.go"}`},
		{"run_command", `{"command":"ls -la internal/tool/"}`},
		{"grep", `{"pattern":"foo","path":"/repo/x.go"}`},
		{"run_command", `{"command":"git log --oneline -5"}`},
		{"read_file", `{"path":"/repo/y.go"}`},
		{"run_command", `{"command":"cat go.mod"}`},
		{"lsp_hover", `{"path":"/repo/z.go","line":1,"character":1}`},
		{"run_command", `{"command":"pwd"}`},
		{"read_file", `{"path":"/repo/w.go"}`},
	}
	var hint string
	for i, st := range steps {
		s.recordToolCall(st.tool, st.args)
		if h := s.check(); h != "" {
			hint = h
			_ = i
			break
		}
	}
	if hint == "" {
		t.Fatal("interleaved diagnostics+observational commands must accumulate and fire (#488): detector was blind")
	}
	if !strings.Contains(hint, "Target Scatter") {
		t.Fatalf("unexpected hint: %s", hint)
	}
}

func TestTargetScatter_RealVerifyCommandClearsWindow(t *testing.T) {
	s := newTargetScatterState()

	// 5 unique diagnostics would fire... except a REAL verify command in
	// between clears the window (convergence signal).
	s.recordToolCall("read_file", `{"path":"/repo/a.go"}`)
	s.recordToolCall("grep", `{"path":"/repo/b.go","pattern":"x"}`)
	s.recordToolCall("read_file", `{"path":"/repo/c.go"}`)
	s.recordToolCall("run_command", `{"command":"go build ./..."}`) // verify → clear
	if len(s.uniqueTargets) != 0 {
		t.Fatalf("go build must clear the window, got %d targets", len(s.uniqueTargets))
	}
	s.recordToolCall("read_file", `{"path":"/repo/d.go"}`)
	s.recordToolCall("read_file", `{"path":"/repo/e.go"}`)
	s.recordToolCall("read_file", `{"path":"/repo/f.go"}`)
	if h := s.check(); h != "" {
		t.Fatalf("window was cleared by verify; 3 more targets must not fire, got: %s", h)
	}
}

func TestTargetScatter_MutationClassificationComplete(t *testing.T) {
	// #488: these mutations were unclassified — a REAL mutation left the
	// scatter window alive and the detector later fired "without converging
	// or taking action", contradicting its own contract.
	for _, tool := range []string{
		"file_ops", "undo_edit", "write_command_input", "enter_worktree",
		"git_add", "git_commit", "git_revert", "git_reset", "git_checkout", "git_stash",
	} {
		s := newTargetScatterState()
		s.recordToolCall("read_file", `{"path":"/repo/a.go"}`)
		s.recordToolCall("read_file", `{"path":"/repo/b.go"}`)
		s.recordToolCall(tool, `{}`)
		if len(s.uniqueTargets) != 0 {
			t.Errorf("%s is a mutation and must clear the window", tool)
		}
	}
}

func TestTargetScatter_HasMutationDeadCodeRemoved(t *testing.T) {
	s := newTargetScatterState()
	// The set-then-clear in the same synchronous call was a dead store; the
	// field is gone now. This test pins the structural fact via behavior:
	// mutation → window cleared; subsequent diagnostics re-accumulate.
	s.recordToolCall("edit_file", `{"path":"/repo/a.go"}`)
	if s.totalCalls == 0 {
		t.Fatal("totalCalls must still advance")
	}
	s.recordToolCall("read_file", `{"path":"/repo/x.go"}`)
	s.recordToolCall("read_file", `{"path":"/repo/y.go"}`)
	if len(s.uniqueTargets) != 2 {
		t.Fatalf("post-mutation diagnostics must re-accumulate, got %d", len(s.uniqueTargets))
	}
}
