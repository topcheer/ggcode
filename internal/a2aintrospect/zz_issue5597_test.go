package a2aintrospect

import (
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
)

// Pin: Sources V3 aggregation - faces filter, brief clipping, cursor contract.
func TestIssue5597_SourcesV3(t *testing.T) {
	all := make([]a2a.RuntimeConversationTurn, 30)
	for i := range all {
		all[i] = a2a.RuntimeConversationTurn{Role: "user", Text: "turn"}
	}
	src := &Sources{
		Status:       func() *a2a.RuntimeStatus { return &a2a.RuntimeStatus{Busy: true} },
		Conversation: ConversationFromTurns(all),
		Schedule:     func() *a2a.RuntimeSchedule { return &a2a.RuntimeSchedule{Todos: []a2a.RuntimeTodo{{ID: "1"}}} },
	}
	// full: default 20 + cursor
	snap, _ := src.RuntimeSnapshotV3(a2a.SnapshotRequest{Depth: a2a.DepthFull})
	if len(snap.Conversation.Turns) != 20 || !snap.Conversation.Truncated || snap.Conversation.NextCursor != "20" {
		t.Fatalf("full window contract: n=%d trunc=%v cur=%q", len(snap.Conversation.Turns), snap.Conversation.Truncated, snap.Conversation.NextCursor)
	}
	// cursor page: remaining 10, no next
	snap, _ = src.RuntimeSnapshotV3(a2a.SnapshotRequest{Depth: a2a.DepthFull, CursorConv: "20"})
	if len(snap.Conversation.Turns) != 10 || snap.Conversation.Truncated {
		t.Fatalf("tail page contract: n=%d trunc=%v", len(snap.Conversation.Turns), snap.Conversation.Truncated)
	}
	// faces filter drops non-requested faces
	snap, _ = src.RuntimeSnapshotV3(a2a.SnapshotRequest{Depth: a2a.DepthFull, Faces: []string{"status"}})
	if snap.Schedule != nil || snap.Conversation != nil || snap.Status == nil {
		t.Fatalf("faces filter broken: %+v", snap)
	}
}
