package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIssue1082_CascadeTierEscillation guards #1082: soft (3) -> hard "ROOT
// CAUSE" (4) -> abort (5) escalation must all fire for one root; the old
// boolean fired gate made tiers 4/5 dead code.
func TestIssue1082_CascadeTierEscillation(t *testing.T) {
	s := newErrorCascadeState()
	msg := `error in "internal/util/util.go"`
	var gotSoft, gotHard, gotAbort bool
	for i := 0; i < 5; i++ {
		g := s.recordError("edit_file", msg)
		if strings.Contains(g, "ABORT") {
			gotAbort = true
		} else if strings.Contains(g, "ROOT CAUSE") {
			gotHard = true
		} else if strings.Contains(g, "[Error Cascade]") {
			gotSoft = true
		}
	}
	if !gotSoft || !gotHard || !gotAbort {
		t.Fatalf("expected all three tiers to fire, got soft=%v hard=%v abort=%v", gotSoft, gotHard, gotAbort)
	}
	// Beyond abort: silence.
	if g := s.recordError("edit_file", msg); g != "" {
		t.Fatalf("expected no re-fire after abort, got %q", g)
	}
}

// TestIssue1084_FileOpsRecursiveDeleteWarned guards #1084: file_ops delete
// with recursive=true must trigger the same critical warning as `rm -rf`.
func TestIssue1084_FileOpsRecursiveDeleteWarned(t *testing.T) {
	a := &Agent{destructiveGuard: newGitDestructiveState()}
	args, _ := json.Marshal(map[string]any{
		"operations": []any{map[string]any{"action": "delete", "source": "/tmp/x", "recursive": true}},
	})
	w := a.checkGitDestructive("file_ops", args)
	if !strings.Contains(w, "recursive delete") && !strings.Contains(w, "CRITICAL") {
		t.Fatalf("expected critical warning for recursive file_ops delete, got %q", w)
	}
	// Non-recursive delete must stay silent (same safety margin as before).
	argsNR, _ := json.Marshal(map[string]any{
		"operations": []any{map[string]any{"action": "delete", "source": "/tmp/x"}},
	})
	if w := a.checkGitDestructive("file_ops", argsNR); w != "" {
		t.Fatalf("expected no warning for non-recursive delete, got %q", w)
	}
	// mkdir must stay silent.
	argsMk, _ := json.Marshal(map[string]any{
		"operations": []any{map[string]any{"action": "mkdir", "source": "/tmp/y"}},
	})
	if w := a.checkGitDestructive("file_ops", argsMk); w != "" {
		t.Fatalf("expected no warning for mkdir, got %q", w)
	}
}
