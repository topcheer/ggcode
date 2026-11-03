package tui

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

	m.subAgentFollow.activeID = "g"

	out := m.renderSubAgentFollowStrip()
	got := strings.Count(out, "│")
	if got != 4 {
		t.Fatalf("sliding window must render 4 separators (between 5 chips), got %d:\n%s", got, out)
	}
}

func TestIssue3431_SeparatorCountNoSlide(t *testing.T) {

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
