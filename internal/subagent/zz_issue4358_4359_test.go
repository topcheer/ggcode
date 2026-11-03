package subagent

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

	var textChars int
	for _, ev := range events {
		if ev.Type == AgentEventText {
			textChars += len(ev.Text)
		}
	}
	if textChars == 0 {
		t.Fatal("#4359 regression: all text dropped at eviction boundary with a reasoning anchor - rescue lost the content")
	}

	if events[0].Type != AgentEventText && textChars < maxAgentEvents/2 {
		t.Fatalf("head is %v with only %d text chars - rescue insertion missing", events[0].Type, textChars)
	}
}
