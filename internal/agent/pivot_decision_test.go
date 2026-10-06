package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func pivotArgs(t *testing.T, cmd string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// r391 (AutoResearchClaw Pivot/Refine): three consecutive failures of one
// command family surface an explicit REPAIR-vs-PIVOT decision prompt; the
// same-family success clears the streak; other tools are ignored.
func TestPivotDecisionFirstPrompt(t *testing.T) {
	p := newPivotDecisionTracker()
	for i := 0; i < 2; i++ {
		p.recordToolCall("run_command", pivotArgs(t, "go test ./internal/agent/ -run X"), true, "exit status 1")
		if h := p.checkAndWarn(); h != "" {
			t.Fatalf("fired early at %d failures", i+1)
		}
	}
	p.recordToolCall("run_command", pivotArgs(t, "go test ./internal/tool/ -run Y"), true, "exit status 1") // same family "go test"
	h := p.checkAndWarn()
	if h == "" {
		t.Fatal("expected first decision prompt at 3 consecutive failures")
	}
	if !strings.Contains(h, "REPAIR") || !strings.Contains(h, "PIVOT") {
		t.Errorf("prompt must force the binary decision: %s", h)
	}
	if !strings.Contains(h, "`go test`") {
		t.Errorf("prompt must name the failing family: %s", h)
	}
	// No re-fire while under the forced threshold.
	if p.checkAndWarn() != "" {
		t.Error("must not emit a second prompt before the forced threshold")
	}
}

func TestPivotDecisionForcedPivot(t *testing.T) {
	p := newPivotDecisionTracker()
	for i := 0; i < 6; i++ {
		p.recordToolCall("run_command", pivotArgs(t, "make verify-ci"), true, "exit status 1")
	}
	first := p.checkAndWarn()
	if !strings.Contains(first, "REPAIR") {
		t.Fatalf("first prompt must be the decision form, got: %s", first)
	}
	// Fails 7 and 8 after the first prompt: escalates to forced PIVOT.
	p.recordToolCall("run_command", pivotArgs(t, "make lint"), true, "exit status 1")
	p.recordToolCall("run_command", pivotArgs(t, "make verify"), true, "exit status 1")
	forced := p.checkAndWarn()
	if !strings.Contains(forced, "PIVOT") || !strings.Contains(forced, "no longer credible") {
		t.Errorf("expected forced-pivot escalation, got: %s", forced)
	}
	// Max 2 emissions per key per run.
	p.recordToolCall("run_command", pivotArgs(t, "make build"), true, "exit status 1")
	if p.checkAndWarn() != "" {
		t.Error("must cap at 2 prompts per command family")
	}
}

func TestPivotDecisionSuccessResets(t *testing.T) {
	p := newPivotDecisionTracker()
	for i := 0; i < 3; i++ {
		p.recordToolCall("run_command", pivotArgs(t, "npm run build"), true, "exit status 1")
	}
	if p.checkAndWarn() == "" {
		t.Fatal("expected prompt at 3 failures")
	}
	p.recordToolCall("run_command", pivotArgs(t, "npm run build"), false, "") // success clears
	p.recordToolCall("run_command", pivotArgs(t, "npm run build"), true, "exit status 1")
	if h := p.checkAndWarn(); h != "" {
		t.Errorf("streak must restart after success, got prompt: %s", h)
	}
}

func TestPivotDecisionKeyExtraction(t *testing.T) {
	cases := []struct {
		tool, args, want string
	}{
		{"edit_file", `{"path":"/x"}`, ""},
		{"run_command", pivotArgs(t, "# build it\ngo build ./..."), "go build"},
		{"run_command", pivotArgs(t, "make verify-ci"), "make verify-ci"},
		{"run_command", pivotArgs(t, "ls"), "ls"},
		{"run_command", `{"command":""}`, ""},
	}
	for _, c := range cases {
		if got := pivotCommandKey(c.tool, c.args); got != c.want {
			t.Errorf("pivotCommandKey(%s) = %q, want %q", c.tool, got, c.want)
		}
	}
}

func TestPivotDecisionReset(t *testing.T) {
	p := newPivotDecisionTracker()
	for i := 0; i < 3; i++ {
		p.recordToolCall("run_command", pivotArgs(t, "go build ./..."), true, "exit status 1")
	}
	p.reset()
	if h := p.checkAndWarn(); h != "" {
		t.Errorf("reset must clear state, got prompt: %s", h)
	}
}
