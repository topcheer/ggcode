package a2aintrospect

import (
	"testing"

	"github.com/topcheer/ggcode/internal/a2a"
)

// Phase-3 pin (#5575 ruling): conversation face passes content through RAW -
// the Sources.Redactor pre-pass must NOT touch it (domain gate = trust
// boundary). Other content faces keep redaction.
func TestIssue5575Phase3ConversationNoRedact(t *testing.T) {
	marker := "PLAINMARKER-not-a-secret"
	s := &Sources{
		Redactor: func(in string) string {
			// Aggressive fake redactor: would mask everything.
			return "[REDACTED]"
		},
		Conversation: func() []a2a.RuntimeConversationTurn {
			return []a2a.RuntimeConversationTurn{{Role: "user", Summary: marker}}
		},
		Recent: func() []a2a.RuntimeToolTrace {
			return []a2a.RuntimeToolTrace{{TS: "-1", Summary: marker}}
		},
	}
	snap, err := s.RuntimeSnapshot(a2a.IntrospectionFull)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap.Conversation) != 1 || snap.Conversation[0].Summary != marker {
		t.Fatalf("conversation content was altered: %+v", snap.Conversation)
	}
	if len(snap.Recent) != 1 || snap.Recent[0].Summary == marker {
		t.Fatalf("recent face must still be redacted: %+v", snap.Recent)
	}
}
