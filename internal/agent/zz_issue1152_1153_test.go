package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// Regression tests for GitHub issues #1152 and #1153.
//
// #1152: prContainsWord treated '.' and '(' as word separators, so
// qualified/method call forms (os.Rename(, x.rename(), df.rename) hit
// refactoring keywords whole-word even though the edit merely CALLS an API
// named "rename" instead of restructuring anything.
//
// #1153: wait_command / read_command_output / task_output cleared the
// verification counters for ANY background job with no command gating, and
// graded success from IsError, which describes the wait action rather than
// the job's exit outcome.

// ---------- helpers ----------

// zzJobSnap renders a minimal command-job snapshot mirroring the tool
// layer's formatCommandJobSnapshot layout ("Job ID:" / "Status:" headers).
func zzJobSnap(jobID, status string, outLines ...string) string {
	sb := "Job ID: " + jobID + "\nStatus: " + status + "\nDuration: 1s\nTimeout: none (detached)\nTotal lines: 2\nRecent output:\n"
	for _, l := range outLines {
		sb += l + "\n"
	}
	return sb
}

// ---------- #1152: refactoring-intent word boundaries ----------

func TestZZIssue1152PrContainsWordQualifiedAndCallForms(t *testing.T) {
	cases := []struct {
		text string
		kw   string
		want bool
	}{
		// Qualified / method-call shapes introduced by #1152.
		{"return os.rename(src, dst)", "rename", false},
		{"value, err := os.rfdrename(a, b)", "rfdrename", false}, // suffix guard intact
		{"x.rename()", "rename", false},
		{"df.rename(cols)", "rename", false},
		{"path.Rename(", "Rename", false}, // '.' + '(' both deny
		{"cfg.extract(", "extract", false},
		{"s.optimize()", "optimize", false},
		{"svc.consolidate(cfg)", "consolidate", false},
		{"o.reorganize(", "reorganize", false},
		// True intent prose still matches.
		{"we should rename things", "rename", true},
		{"refactor the module now", "refactor", true},
		{"extract the helper", "extract", true},
		// #487 F2 regression guards must keep holding.
		{"renamed value flows on", "rename", false},
		{"extractTargets helper", "extract", false},
		{"abstractHandler caller", "abstract", false},
	}
	for _, c := range cases {
		if got := prContainsWord(c.text, c.kw); got != c.want {
			t.Errorf("prContainsWord(%q, %q) = %v, want %v", c.text, c.kw, got, c.want)
		}
	}
}

func TestZZIssue1152OsRenameEditIsNotRefactoringIntent(t *testing.T) {
	// End-to-end shape from issue #1152: an os.Rename-based fix must not be
	// classified as refactoring intent.
	oldText := "" +
		"// handler keeps the result shape simple today in review\n" +
		"// second padding comment line carrying review context too\n" +
		"// third padding line fills remaining budget expectations\n" +
		"// fourth padding line keeps the diff localized for tests\n" +
		"\treturn nil\n"
	newText := strings.Replace(oldText, "\treturn nil\n", "\treturn os.rename(src, dst)\n", 1)
	args, _ := json.Marshal(map[string]string{
		"file_path": "/x.go", "old_text": oldText, "new_text": newText,
	})
	if classifyRefactorEdit("edit_file", args) {
		t.Fatalf("os.rename fix misclassified as refactoring intent")
	}

	// Control: large rewrite hitting the keyword via unqualified prose
	// still classifies as refactoring intent.
	largeOld := "alpha step rewrites layout fully here right now\n" +
		"beta step moves blocks around completely today\n" +
		"gamma step cleans dead branches up for good\n" +
		"value, err := prepare(stage)\n" +
		"return finalize(value, stage)"
	ctrlArgs, _ := json.Marshal(map[string]string{
		"file_path": "/y.go",
		"old_text":  largeOld,
		"new_text": "todo rename legacy names across module scope\n" +
			"one step moves blocks around completely today\n" +
			"two step cleans dead branches up for good time\n" +
			"vals, err := prepare(stage)\n" +
			"return finalize(vals, stage)",
	})
	if !classifyRefactorEdit("edit_file", ctrlArgs) {
		t.Fatalf("genuine rewrite with rename keyword not detected as refactoring intent")
	}
}

// ---------- #1153: background job verification attribution ----------

func TestZZIssue1153UnregisteredJobFinishIsNotVerification(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/a.go"}, false, "")
	s.recordToolCall("wait_command", map[string]interface{}{"job_id": "cmd-zzz"}, false, zzJobSnap("cmd-zzz", "completed"))
	if s.editsSinceVerify != 1 {
		t.Fatalf("editsSinceVerify = %d, want 1 (unregistered job must not clear)", s.editsSinceVerify)
	}
	if s.everVerified {
		t.Fatalf("everVerified = true after unrelated job completed")
	}
	if hint := s.checkSuccessClaim("All tests pass."); hint == "" {
		t.Fatalf("success claim not flagged despite no real verification")
	}
}

func TestZZIssue1153StartCommandRegistersButDoesNotVerify(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/a.go"}, false, "")

	// Non-verify start registers the job but changes nothing else (#1153:
	// launching is not verifying, same precedence correctionSpiral uses).
	s.recordToolCall("start_command",
		map[string]interface{}{"command": "npm run dev"}, false,
		zzJobSnap("cmd-1", "running"))
	rec, ok := s.backgroundJobs["cmd-1"]
	if !ok {
		t.Fatalf("start_command result not registered by job id")
	}
	if rec.isVerify {
		t.Fatalf("npm run dev wrongly marked isVerify")
	}
	if s.editsSinceVerify != 1 || s.everVerified || s.lastVerifyFailed {
		t.Fatalf("start_command mutated state: edits=%d verified=%v failed=%v",
			s.editsSinceVerify, s.everVerified, s.lastVerifyFailed)
	}

	// Verify-flavored start registers with isVerify=true but STILL does not
	// verify on launch.
	s.recordToolCall("start_command",
		map[string]interface{}{"command": "go test ./..."}, false,
		zzJobSnap("cmd-2", "running"))
	if rec := s.backgroundJobs["cmd-2"]; !rec.isVerify {
		t.Fatalf("go test background job should be marked isVerify")
	}
	if s.everVerified {
		t.Fatalf("starting go test counted as verification before any result")
	}

	// A failing start_command (spawn error) must not fabricate failure either.
	s.recordToolCall("start_command",
		map[string]interface{}{"command": "go vet ./..."}, true, "manager unavailable")
	if s.lastVerifyFailed {
		t.Fatalf("failed start_command fabricated a failed verification")
	}
}

func TestZZIssue1153BackgroundFailThenPassGrading(t *testing.T) {
	// Failed background go test must read as FAILED verification even though
	// the wait_command itself succeeded (its IsError is false) - #1153.
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/a.go"}, false, "")
	s.recordToolCall("start_command",
		map[string]interface{}{"command": "go test ./..."}, false, zzJobSnap("cmd-f", "running"))
	s.recordToolCall("wait_command", map[string]interface{}{"job_id": "cmd-f"}, false,
		zzJobSnap("cmd-f", "failed", "FAIL\texample.com/m/pkg\t0.5s"))
	if !s.lastVerifyFailed {
		t.Fatalf("failed background test not recorded as failed verification")
	}
	if s.lastVerifyFailedCmd != "go test ./..." {
		t.Fatalf("lastVerifyFailedCmd = %q, want originating command", s.lastVerifyFailedCmd)
	}
	if s.editsSinceVerify != 1 {
		t.Fatalf("editsSinceVerify = %d, want 1 (failure must not clear)", s.editsSinceVerify)
	}
	hint := s.checkSuccessClaim("All tests pass.")
	if !strings.Contains(hint, "CRITICAL") {
		t.Fatalf("claim contradicting observed failure not CRITICAL, got %q", hint)
	}

	// Passing background make test clears state only when the JOB completed.
	s2 := newPrematureSuccessState()
	s2.recordToolCall("edit_file", map[string]interface{}{"file_path": "/b.go"}, false, "")
	s2.recordToolCall("start_command",
		map[string]interface{}{"command": "make test"}, false, zzJobSnap("cmd-p", "running"))
	s2.recordToolCall("read_command_output", map[string]interface{}{"job_id": "cmd-p"}, false,
		zzJobSnap("cmd-p", "completed", "ok \texample.com/m/pkg\t0.4s"))
	if s2.editsSinceVerify != 0 || !s2.everVerified || s2.lastVerifyFailed {
		t.Fatalf("completed verify job did not clear state: edits=%d verified=%v failed=%v",
			s2.editsSinceVerify, s2.everVerified, s2.lastVerifyFailed)
	}
	// Entry consumed once terminal; re-waiting must not re-grade silently.
	s2.recordToolCall("read_command_output", map[string]interface{}{"job_id": "cmd-p"}, false,
		zzJobSnap("cmd-p", "completed"))
	if s2.lastVerifyFailed {
		t.Fatalf("repeated wait of consumed entry changed state")
	}
}

func TestZZIssue1153RunningPollAndUnknownStatusConservative(t *testing.T) {
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", map[string]interface{}{"file_path": "/a.go"}, false, "")
	s.recordToolCall("start_command",
		map[string]interface{}{"command": "go build ./..."}, false, zzJobSnap("cmd-r", "running"))

	// Poll returning "running": indeterminate, no state change (#1153).
	s.recordToolCall("wait_command", map[string]interface{}{"job_id": "cmd-r"}, false,
		zzJobSnap("cmd-r", "running", "still compiling"))
	if s.everVerified || s.lastVerifyFailed || s.editsSinceVerify != 1 {
		t.Fatalf("running poll mutated state: edits=%d verified=%v failed=%v",
			s.editsSinceVerify, s.everVerified, s.lastVerifyFailed)
	}

	// task_output using task_id with no parseable Status header: also
	// indeterminate and conservative.
	s.recordToolCall("task_output", map[string]interface{}{"task_id": "cmd-r"}, false,
		"{\"result\":\"some json payload\"}")
	if s.everVerified || s.lastVerifyFailed {
		t.Fatalf("unparsable status mutated state")
	}

	// Then the completion arrives through the same id and grades normally.
	s.recordToolCall("task_output", map[string]interface{}{"task_id": "cmd-r"}, false,
		zzJobSnap("cmd-r", "completed"))
	if !s.everVerified || s.editsSinceVerify != 0 {
		t.Fatalf("late completion not graded: verified=%v edits=%d", s.everVerified, s.editsSinceVerify)
	}
}

func TestZZIssue1153RunCommandFailureStillCritical(t *testing.T) {
	// Direct foreground run_command failures keep their #350 semantics under
	// the new signature (regression parity).
	s := newPrematureSuccessState()
	s.recordToolCall("edit_file", nil, false, "")
	s.recordToolCall("run_command", map[string]interface{}{"command": "make check"}, true, "exit status 2")
	if !s.lastVerifyFailed {
		t.Fatalf("foreground failure not recorded")
	}
	if hint := s.checkSuccessClaim("The task is complete."); !strings.Contains(hint, "CRITICAL") {
		t.Fatalf("contradicting claim not CRITICAL, got %q", hint)
	}
}
