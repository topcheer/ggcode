package agent

// zz_issue4340_test.go -- regression nail for #4340: the "leave X alone"
// arm of refusalNegatePattern was `leave\b[^n]{0,5}alone`, whose gap cap
// (object ≤3 chars after the two boundary spaces) plus the [^n] exclusion
// made every real object ("them", "that", any filename containing 'n' or
// longer than 3 chars) miss, so "leave config.yaml alone" never entered
// the 30-day enforcement ledger. The reaffirm arm's 10-char gap had the
// same disease on the release side.

import (
	"strings"
	"testing"
)

func TestIssue4340LeaveAloneExtraction(t *testing.T) {
	mustExtract := []string{
		"leave them alone, please",
		"leave that alone, please",
		"leave config.yaml alone, please",
		"leave main.go alone, please",
		"leave nginx.conf alone, please",
		"leave internal/agent/agent.go alone, please",
		"leave it alone, please", // pre-widening survivor, must stay
		"leave alone, please",    // zero-object form
	}
	for _, s := range mustExtract {
		if got := extractRefusals(s); len(got) == 0 {
			t.Errorf("%q: extractRefusals empty - leave-alone arm still dead", s)
		}
	}
	mustNot := []string{
		"leaven the dough alone for now", // 'leave' inside 'leaven': no \b, no match
		"leave the room and close the door",
	}
	for _, s := range mustNot {
		if got := extractRefusals(s); len(got) != 0 {
			t.Errorf("%q: unexpected extraction %v", s, got)
		}
	}
}

// TestIssue4340LeaveAloneEnforces: extraction must reach the enforcement
// path - a recorded "leave <file> alone" must block a later write to that
// file (the exact silent-failure the issue reports).
func TestIssue4340LeaveAloneEnforces(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("leave config.yaml alone, please", "nl", refusalKindDirect)
	if msg := l.checkBlocked("run_command", `{"command":"rm config.yaml"}`); msg == "" {
		t.Fatal("recorded 'leave config.yaml alone' must block rm config.yaml")
	} else if !strings.Contains(msg, "refusal") {
		t.Fatalf("block must come from refusal ledger, got %q", msg)
	}
}

// TestIssue4340ReaffirmCoversWidenedGap: the #3469 reaffirm guard must veto
// lifts whose text continues the refusal with the now-reachable longer
// "leave <file> alone" phrasing (release-side companion of the same fix).
func TestIssue4340ReaffirmCoversWidenedGap(t *testing.T) {
	for _, s := range []string{
		"leave config.yaml alone",
		"leave main.go alone",
	} {
		if !refusalReaffirmPattern.MatchString(s) {
			t.Errorf("reaffirm pattern must match %q - a strong-lift release carrying this phrasing would silently delete a live block", s)
		}
	}
}
