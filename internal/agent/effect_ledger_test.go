package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func mkCmdArgs(command, workDir string) []byte {
	b, _ := json.Marshal(struct {
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
	}{Command: command, WorkingDir: workDir})
	return b
}

func TestClassifyEffectOutcome(t *testing.T) {
	cases := []struct {
		name     string
		res      tool.Result
		wantRec  bool
		wantOutc effectOutcome
	}{
		{"success not recorded", tool.Result{Content: "ok"}, false, 0},
		{"job timeout uncertain", tool.Result{IsError: true, Content: "command timed out after 30m0s"}, true, effectUncertain},
		{"ctx deadline uncertain", tool.Result{IsError: true, Content: "Command failed: context deadline exceeded"}, true, effectUncertain},
		{"killed uncertain", tool.Result{IsError: true, Content: "Command failed: signal: killed"}, true, effectUncertain},
		{"canceled uncertain", tool.Result{IsError: true, Content: "Command failed: context canceled"}, true, effectUncertain},
		{"plain failure", tool.Result{IsError: true, Content: "STDERR:\nerror: failed to push some refs\nCommand failed: exit status 1"}, true, effectFailed},
		{"permission denial excluded", tool.Result{IsError: true, Content: "Permission denied for tool \"run_command\". User rejected the request."}, false, 0},
		{"invalid input excluded", tool.Result{IsError: true, Content: "invalid input: unexpected end of JSON input"}, false, 0},
		{"gate block excluded", tool.Result{IsError: true, Content: "Command blocked: rm -rf / (dangerous command)"}, false, 0},
		{"shell resolve excluded", tool.Result{IsError: true, Content: "failed to resolve shell: no such file"}, false, 0},
	}
	for _, tc := range cases {
		outcome, rec := classifyEffectOutcome(tc.res)
		if rec != tc.wantRec || (rec && outcome != tc.wantOutc) {
			t.Errorf("%s: got (rec=%v outcome=%v), want (rec=%v outcome=%v)",
				tc.name, rec, outcome, tc.wantRec, tc.wantOutc)
		}
	}
}

// The retry hint ("[Effect Ledger] this exact command already ran...") was
// removed: a repeated failing command must not produce any guidance text.
func TestRecordEffectAttemptNeverHints(t *testing.T) {
	a := &Agent{}
	a.recordEffectAttempt("run_command", mkCmdArgs("git push origin main", "/repo"),
		tool.Result{IsError: true, Content: "command timed out after 30m0s"})
	// Second, identical attempt: previously this returned the Effect Ledger
	// hint. Nothing to assert on a void method beyond "does not panic and
	// no result is appended"; the compile-time signature is the contract.
	a.recordEffectAttempt("run_command", mkCmdArgs("git push origin main", "/repo"),
		tool.Result{IsError: true, Content: "Command failed: exit status 1"})
}

func TestRecordEffectAttemptIgnoresOtherTools(t *testing.T) {
	a := &Agent{}
	a.recordEffectAttempt("edit_file", []byte(`{}`), tool.Result{IsError: true, Content: "timed out after 1s"})
	a.recordEffectAttempt("run_command", []byte(`not json`), tool.Result{IsError: true, Content: "x"})
}

func TestRecordEffectAttemptClearsBuildIdempotencyOnFailure(t *testing.T) {
	a := &Agent{}
	a.buildIdempot = newBuildIdempotencyState()
	const cmd = "go test ./..."
	isBuild, _, key := detectBuildTestCommand(cmd)
	if !isBuild {
		t.Fatalf("%q must be detected as a build/test command", cmd)
	}
	a.buildIdempot.lastBuildCmd = key
	a.recordEffectAttempt("run_command", mkCmdArgs(cmd, "/repo"),
		tool.Result{IsError: true, Content: "Command failed: exit status 1"})
	if a.buildIdempot.lastBuildCmd != "" {
		t.Fatalf("failed build must clear the idempotency cache, got %q", a.buildIdempot.lastBuildCmd)
	}
	a.buildIdempot.lastBuildCmd = key
	a.recordEffectAttempt("run_command", mkCmdArgs(cmd, "/repo"),
		tool.Result{Content: "ok"})
	if a.buildIdempot.lastBuildCmd != key {
		t.Fatalf("successful build must keep the idempotency cache, got %q", a.buildIdempot.lastBuildCmd)
	}
}
