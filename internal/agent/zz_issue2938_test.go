package agent

import (
	"strings"
	"testing"
)

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
