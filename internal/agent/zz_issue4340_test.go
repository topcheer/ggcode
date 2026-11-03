package agent

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
		"leave it alone, please",
		"leave alone, please",
	}
	for _, s := range mustExtract {
		if got := extractRefusals(s); len(got) == 0 {
			t.Errorf("%q: extractRefusals empty - leave-alone arm still dead", s)
		}
	}
	mustNot := []string{
		"leaven the dough alone for now",
		"leave the room and close the door",
	}
	for _, s := range mustNot {
		if got := extractRefusals(s); len(got) != 0 {
			t.Errorf("%q: unexpected extraction %v", s, got)
		}
	}
}

func TestIssue4340LeaveAloneEnforces(t *testing.T) {
	l := &refusalLedger{workingDir: t.TempDir()}
	l.record("leave config.yaml alone, please", "nl", refusalKindDirect)
	if msg := l.checkBlocked("run_command", `{"command":"rm config.yaml"}`); msg == "" {
		t.Fatal("recorded 'leave config.yaml alone' must block rm config.yaml")
	} else if !strings.Contains(msg, "refusal") {
		t.Fatalf("block must come from refusal ledger, got %q", msg)
	}
}

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
