package agent

// Tests for the effect ledger (side-effect duplicate guard).
//
// The ledger records failed/uncertain shell executions and annotates retries
// of the identical command so the model verifies external state instead of
// treating the retry output as authoritative.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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

func TestEffectLedgerRetryHint(t *testing.T) {
	e := newEffectLedger()

	// No prior attempt -> no hint.
	if h := e.priorHint("git push origin main", "/repo"); h != "" {
		t.Fatalf("no prior attempt: got hint %q", h)
	}

	// A failed attempt for a different command must not leak.
	e.record("make test", "/repo", effectFailed)
	if h := e.priorHint("git push origin main", "/repo"); h != "" {
		t.Fatalf("different command: got hint %q", h)
	}

	e.record("git push origin main", "/repo", effectUncertain)
	h := e.priorHint("git push origin main", "/repo")
	if h == "" {
		t.Fatal("retry after uncertain attempt: want hint, got none")
	}
	if !strings.Contains(h, "Effect Ledger") || !strings.Contains(h, "timed out") {
		t.Errorf("hint text incomplete: %q", h)
	}

	// Different workDir is a different effect key.
	if h := e.priorHint("git push origin main", "/other"); h != "" {
		t.Fatalf("different workDir: got hint %q", h)
	}

	// Successes are never recorded, so they never produce hints.
	e.record("make lint", "/repo", effectFailed)
	_ = e.priorHint("make lint", "/repo") // consumes nothing; records are not deleted
}

func TestEffectLedgerWindow(t *testing.T) {
	e := newEffectLedger()
	base := time.Now()
	e.now = func() time.Time { return base }
	e.record("npm publish", "/pkg", effectFailed)

	e.now = func() time.Time { return base.Add(effectRetryWindow - time.Minute) }
	if h := e.priorHint("npm publish", "/pkg"); h == "" {
		t.Error("inside window: want hint")
	}

	e.now = func() time.Time { return base.Add(effectRetryWindow + time.Minute) }
	if h := e.priorHint("npm publish", "/pkg"); h != "" {
		t.Errorf("outside window: want no hint, got %q", h)
	}
}

func TestEffectLedgerWarningCap(t *testing.T) {
	orig := maxEffectWarnings
	maxEffectWarnings = 2
	defer func() { maxEffectWarnings = orig }()

	e := newEffectLedger()
	e.record("terraform apply", "/infra", effectUncertain)
	for i := 0; i < 3; i++ {
		h := e.priorHint("terraform apply", "/infra")
		if i < 2 && h == "" {
			t.Fatalf("warning %d: want hint", i)
		}
		if i == 2 && h != "" {
			t.Fatalf("warning %d: want capped empty, got %q", i, h)
		}
	}
}

func TestRecordEffectAttemptSelfNotHinted(t *testing.T) {
	a := &Agent{effectLedger: newEffectLedger()}
	args := mkCmdArgs("git push origin main", "/repo")

	// First attempt (times out): no hint, but recorded.
	hint := a.recordEffectAttempt("run_command", args,
		tool.Result{IsError: true, Content: "command timed out after 30m0s"})
	if hint != "" {
		t.Fatalf("first attempt must not hint itself, got %q", hint)
	}

	// Identical retry: hint present even though this attempt also failed.
	hint = a.recordEffectAttempt("run_command", args,
		tool.Result{IsError: true, Content: "Command failed: exit status 1"})
	if hint == "" || !strings.Contains(hint, "Effect Ledger") {
		t.Fatalf("retry: want Effect Ledger hint, got %q", hint)
	}
}

func TestRecordEffectAttemptIgnoresOtherTools(t *testing.T) {
	a := &Agent{effectLedger: newEffectLedger()}
	if h := a.recordEffectAttempt("edit_file", []byte(`{}`), tool.Result{IsError: true, Content: "timed out after 1s"}); h != "" {
		t.Fatalf("non-run_command must be ignored, got %q", h)
	}
	if h := a.recordEffectAttempt("run_command", []byte(`not json`), tool.Result{IsError: true, Content: "x"}); h != "" {
		t.Fatalf("unparseable args must be ignored, got %q", h)
	}
}

func TestEffectKeyStripsLeadingComment(t *testing.T) {
	a := &Agent{effectLedger: newEffectLedger()}
	// The mandated '# activity' comment line (#1530) must not defeat matching.
	a.recordEffectAttempt("run_command", mkCmdArgs("# pushing changes\ngit push origin main", "/repo"),
		tool.Result{IsError: true, Content: "Command failed: context deadline exceeded"})
	hint := a.recordEffectAttempt("run_command", mkCmdArgs("git push origin main", "/repo"),
		tool.Result{IsError: true, Content: "Command failed: exit status 1"})
	if hint == "" {
		t.Fatal("comment-stripped retry: want hint")
	}
}
