package agentruntime

// Tests for issue #1158: teammate_working must not replay the teammate's
// historical text tail under a fixed msgID; each new task must produce its
// own message identity so mobile clients (which join chunks by msgID and do
// not dedup) stop seeing repeated tails.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/swarm"
)

func TestWorkingTaskStartMessageNeverReplaysStaleHistoryTail(t *testing.T) {
	snap := swarm.TeammateSnapshot{
		ID:          "tm-7",
		Name:        "worker",
		CurrentTask: "task one",
		// Historical buffer holds the previous task's final text - the exact
		// data that used to be replayed verbatim by teammate_working.
		Events: []swarm.TeammateEvent{
			{Type: swarm.TeammateEventText, Text: "stale tail from previous task"},
		},
	}

	msgID, text, ok := workingTaskStartMessage(snap)
	if !ok {
		t.Fatal("expected announcement for non-empty current task")
	}
	if strings.Contains(text, "stale tail from previous task") {
		t.Fatalf("announcement must not contain stale history text, got %q", text)
	}
	if !strings.Contains(text, "[task started] task one") {
		t.Fatalf("announcement should state the new task, got %q", text)
	}
	if !strings.HasPrefix(msgID, "tm-tm-7-task-") {
		t.Fatalf("msgID should be per-task, got %q", msgID)
	}
}

func TestWorkingTaskStartMessageDistinctPerConsecutiveTasks(t *testing.T) {
	prevEvents := []swarm.TeammateEvent{
		{Type: swarm.TeammateEventText, Text: "task one output"},
	}
	taskOne := swarm.TeammateSnapshot{
		ID:          "tm-3",
		CurrentTask: "first task",
		Events:      prevEvents,
	}
	id1, _, _ := workingTaskStartMessage(taskOne)

	// Second task on the same teammate: history grew with task one's result.
	taskTwo := swarm.TeammateSnapshot{
		ID:          "tm-3",
		CurrentTask: "second task",
		Events:      append(prevEvents, swarm.TeammateEvent{Type: swarm.TeammateEventText, Text: "second done"}),
	}
	id2, text2, ok := workingTaskStartMessage(taskTwo)
	if !ok {
		t.Fatal("expected announcement for second task")
	}
	if id1 == id2 {
		t.Fatalf("consecutive tasks must get distinct message IDs, both %q", id1)
	}
	if strings.Contains(text2, "task one output") {
		t.Fatalf("second task announcement leaks first task history: %q", text2)
	}

	// Same snapshot state is deterministic (idempotent re-push joins into the
	// same mobile-side message instead of creating duplicates).
	id1b, _, _ := workingTaskStartMessage(taskOne)
	if id1 != id1b {
		t.Fatalf("same task state must yield stable msgID: %q vs %q", id1, id1b)
	}
}

func TestWorkingTaskStartMessageSkipsEmptyTask(t *testing.T) {
	if _, _, ok := workingTaskStartMessage(swarm.TeammateSnapshot{ID: "tm-1"}); ok {
		t.Fatal("empty current task must not produce an announcement")
	}
	if _, _, ok := workingTaskStartMessage(swarm.TeammateSnapshot{ID: "tm-1", CurrentTask: "   "}); ok {
		t.Fatal("whitespace-only current task must not produce an announcement")
	}
}
