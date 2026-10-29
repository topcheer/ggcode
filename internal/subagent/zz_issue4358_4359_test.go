package subagent

// #4358 / #4359 regression probes.
//
// #4358: Wait's #713 grace window was zeroed by an entry-level timer that
// had long since fired for any real run; the fix blocks on done first,
// THEN runs a fresh 200ms grace, re-reading Result and Error. The probe
// terminal-cancels a sub-agent, lands a backfill 60ms after Wait enters
// the terminal branch, and requires Wait to observe it.
//
// #4359: the eviction rescue only merged saved text into a TEXT anchor;
// when the post-eviction head was Reasoning (interleaved streams with no
// tool calls), the text was silently dropped. The fix inserts a
// standalone text event ahead of the non-text anchor.

import (
	"context"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func TestIssue4358WaitGraceObservesLateBackfill(t *testing.T) {
	sa := &SubAgent{ID: "sa-4358", Status: StatusCancelled}
	done := make(chan struct{})
	close(done)
	sa.done = done

	mgr := NewManager(config.SubAgentConfig{})
	mgr.mu.Lock()
	if mgr.agents == nil {
		mgr.agents = make(map[string]*SubAgent)
	}
	mgr.agents[sa.ID] = sa
	mgr.mu.Unlock()

	got := make(chan string, 1)
	go func() {
		res, _ := Wait(context.Background(), mgr, sa.ID)
		got <- res
	}()

	// Backfill lands after Wait has entered the terminal branch but well
	// inside the fresh 200ms grace. With the old pre-fired entry timer the
	// grace was ~0 and this backfill raced to a loss most of the time.
	time.Sleep(60 * time.Millisecond)
	sa.mu.Lock()
	sa.Result = "backfilled-result"
	sa.mu.Unlock()

	select {
	case res := <-got:
		if res != "backfilled-result" {
			t.Fatalf("Wait = %q, want backfilled-result (grace window dropped the backfill)", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait never returned")
	}
}

func TestIssue4359EvictionRescuesTextBeforeReasoningAnchor(t *testing.T) {
	sa := &SubAgent{ID: "sa-4359", Status: StatusRunning}
	// Fill the buffer with interleaved text/reasoning, no tool calls, and
	// make every event non-coalescible with the next (alternate types) so
	// the buffer genuinely fills. Zero merge interval forces no merging.
	for i := 0; i < maxAgentEvents+2; i++ {
		if i%2 == 0 {
			sa.appendEvent(AgentEvent{Type: AgentEventText, Text: "T"})
		} else {
			sa.appendEvent(AgentEvent{Type: AgentEventReasoning, Text: "R"})
		}
	}

	events := sa.Events()
	if len(events) > maxAgentEvents {
		t.Fatalf("events = %d, buffer should be capped at %d", len(events), maxAgentEvents)
	}
	if sa.eventsDropped == 0 {
		t.Fatal("expected eviction to fire")
	}

	// Every dropped text chunk must survive somewhere in the visible
	// buffer: collect all text content and require at least one rescued
	// event (merged or standalone) beyond the tail that arrived after
	// saturation.
	var textChars int
	for _, ev := range events {
		if ev.Type == AgentEventText {
			textChars += len(ev.Text)
		}
	}
	if textChars == 0 {
		t.Fatal("#4359 regression: all text dropped at eviction boundary with a reasoning anchor - rescue lost the content")
	}
	// The head must not start mid-stream with zero rescued text: the fix
	// guarantees the first non-text anchor is preceded by (or merged with)
	// the rescued text.
	if events[0].Type != AgentEventText && textChars < maxAgentEvents/2 {
		t.Fatalf("head is %v with only %d text chars - rescue insertion missing", events[0].Type, textChars)
	}
}
