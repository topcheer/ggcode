package subagent

import (
	"fmt"
	"strings"
	"testing"
)

func TestAppendEventEvictionPreservesSalvagedTextInterleaved(t *testing.T) {
	s := &SubAgent{ID: "sa-4359", Status: StatusRunning}

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

	s.appendEvent(AgentEvent{Type: AgentEventReasoning, Text: "TRIGGER-R"})

	s.appendEvent(AgentEvent{Type: AgentEventText, Text: "TRIGGER-T"})

	var want strings.Builder
	for i := 1; i < maxAgentEvents/10; i += 2 {
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

	if len(events) < 2 || events[1].Type != AgentEventReasoning || events[1].Text != "R040" {
		if len(events) < 2 {
			t.Fatal("expected a second event after the salvaged text")
		}
		t.Fatalf("second event should be Reasoning R040, got type %d text %q", events[1].Type, events[1].Text)
	}

	if last := events[len(events)-1]; last.Type != AgentEventText || last.Text != "TRIGGER-T" {
		t.Fatalf("tail event lost: got type %d text %q", last.Type, last.Text)
	}

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

func TestAppendEventEvictionStillMergesIntoTextHead(t *testing.T) {
	s := &SubAgent{ID: "sa-4359b", Status: StatusRunning}

	for i := 0; i < maxAgentEvents; i++ {
		if i%2 == 0 {
			s.appendEvent(AgentEvent{Type: AgentEventText, Text: fmt.Sprintf("T%03d", i)})
		} else {
			s.appendEvent(AgentEvent{Type: AgentEventReasoning, Text: fmt.Sprintf("R%03d", i)})
		}
	}
	s.appendEvent(AgentEvent{Type: AgentEventText, Text: "TRIGGER"})

	var want strings.Builder
	for i := 0; i < maxAgentEvents/10; i += 2 {
		want.WriteString(fmt.Sprintf("T%03d", i))
	}
	want.WriteString("T040")

	events := s.Events()
	if events[0].Type != AgentEventText || events[0].Text != want.String() {
		t.Fatalf("merge path regressed: got type %d text %q, want merged %q", events[0].Type, events[0].Text, want.String())
	}
}
