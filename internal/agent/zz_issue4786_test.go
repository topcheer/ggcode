package agent

import (
	"strings"
	"testing"
)

// Issue #4786 (S-15): the agent-side edit_file simulation gate shadowed the
// real tool's richer errors. Non-unique anchors must report match line
// numbers, loose matches must report their line numbers, and not-found errors
// must carry the neighbor hint — matching the real edit_file tool's messages.

func TestIssue4786_SimulateEditFileNonUniqueReportsLineNumbers(t *testing.T) {
	content := "header\nswitch x {\ncase \"save\":\n\tlabel = \"OK\"\n}\nswitch y {\ncase \"save\":\n\tlabel = \"YES\"\n}\nfooter\n"
	_, err := simulateEditFile(content, "case \"save\":", "case \"saveAs\":", false)
	if err == nil {
		t.Fatal("expected uniqueness error from simulation gate")
	}
	msg := err.Error()
	if !strings.Contains(msg, "found 2 times") {
		t.Errorf("expected match count, got: %s", msg)
	}
	if !strings.Contains(msg, "Matches start at line(s): 3, 7.") {
		t.Errorf("expected match line numbers 3, 7 from sim gate, got: %s", msg)
	}
}

func TestIssue4786_SimulateEditFileLooseMatchReportsLineNumbers(t *testing.T) {
	// Whitespace-tolerant matching sees both blocks as loose matches while
	// no byte-exact occurrence exists (mixed indent widths).
	content := "alpha\n\t\tif x {\n\t\t\tdo()\n\t\t}\nbeta\n\tif x {\n\t\tdo()\n\t}\ngamma\n"
	_, err := simulateEditFile(content, "\t if x {\n\t\t do()\n\t }", "REPLACED", false)
	if err == nil {
		t.Fatal("expected loose-match uniqueness error from simulation gate")
	}
	msg := err.Error()
	if !strings.Contains(msg, "whitespace-tolerant matching") {
		t.Errorf("expected whitespace-tolerant error, got: %s", msg)
	}
	if !strings.Contains(msg, "Loose matches start at line(s): 2, 6.") {
		t.Errorf("expected loose match line numbers in sim gate error, got: %s", msg)
	}
}

func TestIssue4786_SimulateEditFileNotFoundCarriesNeighborHint(t *testing.T) {
	content := "func alpha() {}\nfunc zeta() {extra}\nfunc gamma() {}\n"
	_, err := simulateEditFile(content, "func zeta() {}", "X", false)
	if err == nil {
		t.Fatal("expected not-found error from simulation gate")
	}
	msg := err.Error()
	if !strings.Contains(msg, "old_text not found in file") {
		t.Errorf("expected not-found prefix, got: %s", msg)
	}
	// The real tool appends neighbor guidance (nearest matching lines); the
	// sim gate must no longer shadow it.
	if !strings.Contains(msg, "nearest matching lines in file:") {
		t.Errorf("expected neighbor hint in sim gate error, got: %s", msg)
	}
}
