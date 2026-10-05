package tui

import (
	"strings"
	"testing"
)

// #3431 probe: once the strip window slides (active slot beyond the head
// maxShow chips), every non-last RENDERED chip must carry a "│" separator.
// The old condition compared the global index i against maxShow-1, which is
// only equivalent to "not the last rendered position" when start==0; with
// start>0 the tail chips of the window were glued together.
func TestFollowStripSeparatorInSlidWindow(t *testing.T) {
	m := Model{}
	m.subAgentFollow.slots = make([]followSlot, 7)
	for i := range m.subAgentFollow.slots {
		m.subAgentFollow.slots[i] = followSlot{ID: string(rune('a' + i)), Name: string(rune('A' + i))}
	}
	m.subAgentFollow.activeID = "g" // 7th slot (index 6) -> start = 6-5+1 = 2

	got := m.renderSubAgentFollowStrip()

	// Window is [C,D,E,F,G]: 5 chips -> exactly 4 separators.
	const want = 4
	if n := strings.Count(got, "│"); n != want {
		t.Fatalf("#3431: slid window (%d slots, active=7th) has %d separators, want %d:\n%s",
			len(m.subAgentFollow.slots), n, want, got)
	}
	// The bug's signature: chip text glued without a separator between
	// adjacent rendered chips. "E" is rendered mid-window and must be
	// followed by a separator before "F".
	if e := strings.Index(got, "E"); e >= 0 {
		tail := got[e:]
		f := strings.Index(tail, "F")
		if f > 0 && !strings.Contains(tail[:f], "│") {
			t.Fatalf("#3431: no separator between mid-window chips E and F:\n%s", got)
		}
	} else {
		t.Fatalf("#3431: expected chip E in slid window:\n%s", got)
	}

	// Control: head window (start=0) keeps 4 separators for 5 rendered chips.
	m.subAgentFollow.activeID = "a"
	if n := strings.Count(m.renderSubAgentFollowStrip(), "│"); n != want {
		t.Fatalf("#3431 regression: head window has %d separators, want %d", n, want)
	}
}
