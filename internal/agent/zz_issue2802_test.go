package agent

import (
	"os"
	"strings"
	"testing"
)

// zz_issue2802_test.go guards against the reproducer rerun-discharge escape
// hatch (#2802): when a reproducer was established by a command path (snippet
// recorded), ANY script-shaped run_command - including unrelated test files
// like `node test/unit/foo.test.js` - used to set reranAfterEdit and silence
// the "re-run your reproducer" warning.

// TestIssue2802UnrelatedScriptDoesNotDischarge pins the exact issue scenario.
func TestIssue2802UnrelatedScriptDoesNotDischarge(t *testing.T) {
	snippet := "python3 repro_bug.py"
	if reproducerRerunMatches("node test/unit/foo.test.js", snippet) {
		t.Error("unrelated test script discharged the rerun obligation (#2802 recurrence)")
	}
	if reproducerRerunMatches("bash other.sh", snippet) {
		t.Error("unrelated bash script discharged the rerun obligation")
	}
	if reproducerRerunMatches("go run ./cmd/otherscript/main.go", snippet) {
		t.Error("unrelated go script discharged the rerun obligation")
	}
}

// TestIssue2802SameScriptRerunStillDischarges ensures the fix did not
// over-tighten: re-running the recorded reproducer (exact or with a cd
// prefix/flags) must still qualify.
func TestIssue2802SameScriptRerunStillDischarges(t *testing.T) {
	snippet := "python3 repro_bug.py"
	if !reproducerRerunMatches("python3 repro_bug.py", snippet) {
		t.Error("exact rerun no longer discharges")
	}
	if !reproducerRerunMatches("cd /tmp && python3 repro_bug.py", snippet) {
		t.Error("rerun with cd prefix no longer discharges")
	}
}

// TestIssue2802SnippetlessKeepsShapeHeuristic pins the preserved loose path:
// without a snippet (text-established), script-shaped commands still qualify.
func TestIssue2802SnippetlessKeepsShapeHeuristic(t *testing.T) {
	if !reproducerRerunMatches("node test/unit/foo.test.js", "") {
		t.Error("snippetless path lost the script-shape heuristic")
	}
	if reproducerRerunMatches("git diff", "") {
		t.Error("non-script command qualified on snippetless path")
	}
}

// TestIssue2802FullLifecycleWarningSurvives drives the observe/check loop:
// reproducer established -> edit -> unrelated script -> warning still fires;
// then the real rerun clears it.
func TestIssue2802FullLifecycleWarningSurvives(t *testing.T) {
	s := newReproducerLifecycleState()
	s.observeToolCalls(1, []string{"run_command"}, []string{"python3 repro_bug.py"})
	s.observeToolCalls(2, []string{"edit_file"}, []string{`{"file_path":"src/main.go"}`})
	s.observeToolCalls(3, []string{"run_command"}, []string{"node test/unit/foo.test.js"})
	// Grace period is 2 iterations after the edit (iter 2), so the earliest
	// warning fires at iter 4.
	if msg := s.checkIncomplete(4); msg == "" || !strings.Contains(msg, "repro") {
		t.Errorf("expected re-run warning after unrelated script, got: %q", msg)
	}
	s.observeToolCalls(5, []string{"run_command"}, []string{"python3 repro_bug.py"})
	if msg := s.checkIncomplete(6); msg != "" {
		t.Errorf("warning should clear after real rerun, got: %q", msg)
	}
}

// TestIssue2802NoShapeShortCircuit pins the source-level invariant: the
// shape regex no longer short-circuits ahead of the snippet check.
func TestIssue2802NoShapeShortCircuit(t *testing.T) {
	src, err := os.ReadFile("reproducer_lifecycle.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	fn := src[strings.Index(string(src), "func reproducerRerunMatches"):]
	end := strings.Index(string(fn), "\n}")
	body := string(fn[:end])
	shapeIdx := strings.Index(body, "reproducerCommandRe.MatchString")
	snippetIdx := strings.Index(body, `snippet == ""`)
	if shapeIdx < 0 || snippetIdx < 0 {
		t.Fatal("could not locate both checks in reproducerRerunMatches")
	}
	if shapeIdx < snippetIdx {
		t.Error("shape regex check precedes the snippet check - short-circuit restored (#2802 recurrence)")
	}
}
