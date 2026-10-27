//go:build goolm

package swarm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/task"
)

// errEventAgent mimics #4474: the provider reports a fatal failure as an
// in-stream error event but RunStream itself returns nil.
type errEventAgent struct{}

func (a *errEventAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent)) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "partial work"})
	onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: errors.New("provider exploded")})
	return nil
}

// TestIssue4474_StreamErrorEventPropagatesToTaskErr pins the contract: an
// in-stream StreamEventError with a nil RunStream error must still make
// executeTask return non-nil err - otherwise the claim path marks the task
// Completed and persistTaskResult stores the "[error: ...]" text as
// findings that dependent tasks consume as input (fake-success channel).
func TestIssue4474_StreamErrorEventPropagatesToTaskErr(t *testing.T) {
	tm := &Teammate{ID: "tm-1", Name: "r", Status: TeammateIdle, Inbox: make(chan MailMessage, 4)}
	team := &Team{ID: "team-1", Name: "t", LeaderID: "l", Teammates: map[string]*Teammate{"tm-1": tm}, Tasks: task.NewManager()}

	out, err := executeTask(context.Background(), &errEventAgent{}, MailMessage{Content: "do", Summary: "s"}, tm, nil, team, 0)
	if err == nil {
		t.Fatalf("in-stream error event must propagate to err, got nil (output=%q)", out)
	}
	if !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("err should carry the stream error, got %v", err)
	}
	// The [error: ...] line comes from the event handler; the post-loop
	// append must NOT duplicate it for the propagated path.
	if n := strings.Count(out, "[error: provider exploded]"); n != 1 {
		t.Fatalf("expected exactly one [error: ...] line, got %d in %q", n, out)
	}
	if !strings.Contains(out, "partial work") {
		t.Fatalf("partial output should be preserved, got %q", out)
	}
}
