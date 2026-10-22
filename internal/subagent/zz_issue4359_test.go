package subagent

import (
	"fmt"
	"strings"
	"testing"
)

// TestAppendEventEvictionPreservesSalvagedTextInterleaved pins #4359: when
// the buffer is full of interleaved text/reasoning events and eviction takes
// the no-turn-boundary path (dropIdx == 0), text salvaged from the dropped
// range must survive even when the first remaining event is Reasoning, not
// Text. Before the fix, savedText was only merged when remaining[0] was Text;
// otherwise it was silently dropped.
func TestAppendEventEvictionPreservesSalvagedTextInterleaved(t *testing.T) {
	s := &SubAgent{ID: "sa-4359", Status: StatusRunning}

	// Fill the buffer to maxAgentEvents with an alternating sequence
	// starting with Reasoning (even indices) and Text (odd indices).
	// Alternating types prevents textMergeInterval coalescing, so every
	// append becomes a distinct buffered event regardless of timing.
	for i := 0; i < maxAgentEvents; i++ {
		if i%2 == 0 {
			s.appendEvent(AgentEvent{Type: AgentEventReasoning, Text: fmt.Sprintf("R%03d", i)})
		} else {
			s.appendEvent(AgentEvent{Type: AgentEventText, Text: fmt.Sprintf("T%03d", i)})
		}
	}
	if got := len(s.events); got != maxAgentEvents {
		t.Fatalf("setup: expected %d buffered events, got %d", maxAgentEvents, got)
	}

	// Trigger eviction with one more Reasoning event: it differs in type
	// from the last buffered event (Text T399), so it cannot coalesce and
	// forces a real append. No turn boundary exists in the buffer, so the
	// eviction drops dropIdx = maxAgentEvents/10 = 40 events ([0,40)) and the
	// first remaining event is the Reasoning event at old index 40 - the exact
	// interleaved shape that lost the salvaged text.
	s.appendEvent(AgentEvent{Type: AgentEventReasoning, Text: "TRIGGER-R"})
	// Tail text event completes the text->reasoning->text interleave; it
	// cannot coalesce with the Reasoning trigger either.
	s.appendEvent(AgentEvent{Type: AgentEventText, Text: "TRIGGER-T"})

	var want strings.Builder
	for i := 1; i < maxAgentEvents/10; i += 2 { // odd indices 1..39 in the dropped range
		want.WriteString(fmt.Sprintf("T%03d", i))
	}

	events := s.Events()
	if len(events) == 0 {
		t.Fatal("no events after eviction")
	}
	if events[0].Type != AgentEventText {
		t.Fatalf("first event should be a Text event carrying the salvaged text, got type %d", events[0].Type)
	}
	if events[0].Text != want.String() {
		t.Fatalf("salvaged text lost: got %q, want %q", events[0].Text, want.String())
	}
	// The interleaving order after the salvaged text must be preserved: the
	// first surviving original event is the Reasoning event from old index 40.
	if len(events) < 2 || events[1].Type != AgentEventReasoning || events[1].Text != "R040" {
		if len(events) < 2 {
			t.Fatal("expected a second event after the salvaged text")
		}
		t.Fatalf("second event should be Reasoning R040, got type %d text %q", events[1].Type, events[1].Text)
	}
	// The tail text event must still be present at the end.
	if last := events[len(events)-1]; last.Type != AgentEventText || last.Text != "TRIGGER-T" {
		t.Fatalf("tail event lost: got type %d text %q", last.Type, last.Text)
	}

	// The salvaged text must also be visible through the incremental consumer
	// path (EventsSince with a pre-eviction cursor replays the survivors).
	replayed, total := s.EventsSince(0)
	if len(replayed) == 0 || replayed[0].Text != want.String() {
		if len(replayed) == 0 {
			t.Fatal("EventsSince returned no survivors")
		}
		t.Fatalf("EventsSince lost salvaged text: first text %q, want %q", replayed[0].Text, want.String())
	}
	if total < maxAgentEvents {
		t.Fatalf("absolute total must not fall back below %d, got %d", maxAgentEvents, total)
	}
}

// TestAppendEventEvictionStillMergesIntoTextHead guards the pre-existing
// merge path: when the first remaining event after eviction is Text, the
// salvaged text is still merged into it (no duplicate event is inserted).
func TestAppendEventEvictionStillMergesIntoTextHead(t *testing.T) {
	s := &SubAgent{ID: "sa-4359b", Status: StatusRunning}
	// Alternate starting with Text (even indices) so the first remaining
	// event after eviction (old index 40) is Text.
	for i := 0; i < maxAgentEvents; i++ {
		if i%2 == 0 {
			s.appendEvent(AgentEvent{Type: AgentEventText, Text: fmt.Sprintf("T%03d", i)})
		} else {
			s.appendEvent(AgentEvent{Type: AgentEventReasoning, Text: fmt.Sprintf("R%03d", i)})
		}
	}
	s.appendEvent(AgentEvent{Type: AgentEventText, Text: "TRIGGER"})

	var want strings.Builder
	for i := 0; i < maxAgentEvents/10; i += 2 { // even indices 0..38 dropped, merged with T040
		want.WriteString(fmt.Sprintf("T%03d", i))
	}
	want.WriteString("T040")

	events := s.Events()
	if events[0].Type != AgentEventText || events[0].Text != want.String() {
		t.Fatalf("merge path regressed: got type %d text %q, want merged %q", events[0].Type, events[0].Text, want.String())
	}
}
