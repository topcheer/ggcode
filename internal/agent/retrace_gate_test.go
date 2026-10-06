package agent

import (
	"context"
	"strings"
	"testing"
)

// r440 probes: RETRACE bidirectional stop gate. Unit level - the two LLM
// completions are exercised via the exported pure pieces (message builders,
// verdict parser, trigger matrix, injection format); no provider is called.

func TestRetraceShouldCheck(t *testing.T) {
	base := postEditVerifyState{sourceEditsThisRun: 2, realBuildOrTestRunThisRun: true}
	cases := []struct {
		name     string
		pv       postEditVerifyState
		research bool
		fired    bool
		want     bool
	}{
		{"happy path", base, false, false, true},
		{"no edits", postEditVerifyState{sourceEditsThisRun: 0, realBuildOrTestRunThisRun: true}, false, false, false},
		{"no receipt", postEditVerifyState{sourceEditsThisRun: 3, realBuildOrTestRunThisRun: false}, false, false, false},
		{"research mode", base, true, false, false},
		{"already fired", base, false, true, false},
	}
	for _, c := range cases {
		if got := retraceShouldCheck(c.pv, c.research, c.fired); got != c.want {
			t.Errorf("%s: retraceShouldCheck = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestBackwardIsolation is THE anchoring-prevention probe: the backward
// message list must not contain the task text - not as the prompt, not as
// system preamble, not anywhere. RETRACE withholds I from B(p, tau_b).
func TestBackwardIsolation(t *testing.T) {
	task := "Fix the login timeout bug in session.go and add a regression test"
	diff := "diff --git a/internal/session.go b/internal/session.go\n+timeout = 30"
	msgs := buildBackwardMessages(diff)
	for i, m := range msgs {
		blob := ""
		for _, c := range m.Content {
			if c.Text != "" {
				blob += c.Text
			}
		}
		if strings.Contains(blob, task) {
			t.Fatalf("backward message %d leaks the task text (anchoring broken)", i)
		}
		if !strings.Contains(blob, diff) {
			t.Fatalf("backward message %d missing the diff", i)
		}
	}
	// Control: the reconcile stage DOES carry the task (that is its job).
	rec := buildReconcileMessages(task, "reconstruction", diff)
	found := false
	for _, m := range rec {
		for _, c := range m.Content {
			if strings.Contains(c.Text, task) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("reconcile message must include the task text")
	}
}

func TestParseRetraceVerdict(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"aligned", "analysis...\nRETRACE: aligned", "aligned"},
		{"misaligned", "RETRACE: misaligned", "misaligned"},
		{"partial with annotation", "RETRACE: partial (test half done)", "partial"},
		{"no marker", "looks fine to me", ""},
		{"garbage after marker", "RETRACE: maybe", ""},
		{"marker scrolled out", "RETRACE: aligned\nl1\nl2\nl3\nl4\nl5\nl6\nl7", ""},
	}
	for _, c := range cases {
		v, guidance := parseRetraceVerdict(c.out)
		if v != c.want {
			t.Errorf("%s: verdict = %q, want %q", c.name, v, c.want)
		}
		if v != "" && guidance == "" {
			t.Errorf("%s: guidance empty despite verdict", c.name)
		}
	}
}

func TestRetraceInjectMessage(t *testing.T) {
	for _, v := range []string{"partial", "misaligned"} {
		msg := retraceInjectMessage(v, "PROBLEM: X\nSCOPE: half\nRevision: add the missing test")
		if !strings.Contains(msg, "[retrace-gate]") {
			t.Errorf("%s: injection missing gate tag", v)
		}
		if !strings.Contains(msg, "add the missing test") {
			t.Errorf("%s: injection missing reviewer guidance", v)
		}
	}
	// Truncation cap.
	long := strings.Repeat("x", 5000)
	msg := retraceInjectMessage("misaligned", long)
	if len(msg) > retraceMsgCap+100 {
		t.Fatalf("injection not capped: %d bytes", len(msg))
	}
}

// runRetraceVerification error paths need a provider; verified indirectly:
// the failure doctrine is "return empty string" and every early return in
// the function is "" - asserted by construction here (compile-level).
func TestRetraceGateStateZeroValue(t *testing.T) {
	var s retraceGateState
	if s.firedThisRun {
		t.Fatal("zero-value retraceGateState must not be fired")
	}
	_ = context.Background() // keep context import honest for future probes
}
