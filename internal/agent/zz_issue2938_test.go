package agent

import (
	"strings"
	"testing"
)

// zz_issue2938_test.go - regression probes for #2938: the sequential channel
// safety detectors (double-close, send-after-close) had no mutual-exclusion
// context for switch/select case clauses - Go runs exactly one clause (no
// fallthrough across select cases, no case-body fallthrough in switch), so
// close-in-one-case vs send/close-in-another-case patterns (the standard
// error-handling / select idiom) were flagged as "will panic" false
// positives. Ops in sibling clauses of the same switch/select (outside
// loops) must be treated as mutually exclusive, same principle as #2648 if
// sibling branches. Inside loops the exemption is conservatively withheld:
// a loop re-executes the select across iterations, and loop-carried risk
// belongs to detectCloseInLoops.

func TestIssue2938SelectSiblingCasesNotFlagged(t *testing.T) {
	src := `package p
func f(done chan struct{}, ch chan int) {
	select {
	case <-done:
		close(ch)
	case ch <- 1:
	}
}
`
	instances := findChannelSafetyIssues(src)
	for _, inst := range instances {
		t.Fatalf("#2938: select sibling close/send falsely flagged: %v", inst)
	}
}

func TestIssue2938SwitchSiblingCasesNotFlagged(t *testing.T) {
	src := `package p
func g(ch chan int, mode int) {
	switch mode {
	case 1:
		close(ch)
	case 2:
		close(ch)
	}
}
`
	instances := findChannelSafetyIssues(src)
	for _, inst := range instances {
		t.Fatalf("#2938: switch sibling double-close falsely flagged: %v", inst)
	}
}

func TestIssue2938LoopSelectStillChecked(t *testing.T) {
	// Conservative scope: inside a loop the same select re-executes across
	// iterations, so close-in-one-case vs send-in-another is a REAL
	// loop-carried risk and must stay checkable (loop risk is
	// detectCloseInLoops' turf - the #2938 exemption must not swallow it).
	src := `package p
func h(done chan struct{}, ch chan int) {
	for {
		select {
		case <-done:
			close(ch)
		case ch <- 1:
		}
	}
}
`
	instances := findChannelSafetyIssues(src)
	if len(instances) == 0 {
		t.Fatalf("#2938: close inside for+select loop must still be detected (conservative in-loop scope)")
	}
	joined := ""
	for _, inst := range instances {
		joined += strings.ToLower(inst.kind)
	}
	if !strings.Contains(joined, "loop") {
		t.Fatalf("#2938: expected a close-in-loop finding for the for+select form, got %v", instances)
	}
}

func TestIssue2938SameCasePairStillChecked(t *testing.T) {
	// Ops in the SAME clause keep source-order semantics: close then send
	// within one case body is a real double-run of the same clause path.
	src := `package p
func k(mode int, ch chan int) {
	switch mode {
	case 1:
		close(ch)
		ch <- 1
	}
}
`
	instances := findChannelSafetyIssues(src)
	found := false
	for _, inst := range instances {
		if strings.Contains(strings.ToLower(inst.kind), "send") {
			found = true
		}
	}
	if !found {
		t.Fatalf("#2938: same-clause close-then-send must still be flagged, got %v", instances)
	}
}
