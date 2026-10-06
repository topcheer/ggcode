package tui

// #3431 probe: the follow-strip separator must appear between every pair
// of RENDERED chips. i is a global slot index, the last rendered position
// is start+maxShow-1 (sliding window) - the old absolute `i < maxShow-1`
// dropped separators once the window slid (start>0), gluing chips together.

import (
	"strings"
	"testing"
)

func TestIssue3431_SeparatorCountInSlidingWindow(t *testing.T) {
	m := newTestModel()
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	for _, n := range names {
		m.subAgentFollow.slots = append(m.subAgentFollow.slots, followSlot{ID: n, Name: n})
	}
	// Active on the LAST slot -> window slides to [2,6], per the issue repro.
	m.subAgentFollow.activeID = "g"

	out := m.renderSubAgentFollowStrip()
	got := strings.Count(out, "│")
	if got != 4 {
		t.Fatalf("sliding window must render 4 separators (between 5 chips), got %d:\n%s", got, out)
	}
}

func TestIssue3431_SeparatorCountNoSlide(t *testing.T) {
	// Regression pin: start=0 window still renders maxShow-1 separators.
	m := newTestModel()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		m.subAgentFollow.slots = append(m.subAgentFollow.slots, followSlot{ID: n, Name: n})
	}
	m.subAgentFollow.activeID = "a"
	out := m.renderSubAgentFollowStrip()
	if got := strings.Count(out, "│"); got != 4 {
		t.Fatalf("non-slid window must render 4 separators, got %d:\n%s", got, out)
	}
}
