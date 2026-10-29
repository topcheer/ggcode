package swarm

// Regression test for GitHub issue #4474: providers/relays may surface a
// non-fatal failure as an in-stream StreamEventError while RunStream still
// returns nil. The idle runner previously left err nil in that case, so the
// claim path marked the task Completed and persistTaskResult stored the
// "[error: ...]" text as findings for dependent tasks to inherit. The
// in-stream error must promote to the returned error.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

type streamErrAgent struct{}

func (a *streamErrAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent)) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "partial work"})
	// Relay-style non-fatal in-stream failure, then a clean stream return.
	onEvent(provider.StreamEvent{Type: provider.StreamEventError, Error: errors.New("upstream 429, degraded")})
	return nil
}

func TestIssue4474_StreamErrorEventPromotesToTaskError(t *testing.T) {
	agent := &streamErrAgent{}
	tm := &Teammate{ID: "tm-1", Name: "worker"}
	team := &Team{ID: "team-1"}

	out, err := executeTask(context.Background(), agent, MailMessage{Content: "do work"}, tm, nil, team, 0)
	if err == nil {
		t.Fatalf("executeTask must surface the in-stream error, got nil (output: %q)", out)
	}
	if !strings.Contains(err.Error(), "upstream 429") {
		t.Errorf("promoted error should carry the stream error text, got %v", err)
	}
	// The [error: ...] line must appear exactly once (in-stream append, not
	// duplicated by the err != nil branch).
	if n := strings.Count(out, "[error: upstream 429, degraded]"); n != 1 {
		t.Errorf("output should contain exactly one [error: ...] line, got %d in %q", n, out)
	}
}

// Clean streams must stay unaffected: no error, no injected markers.
type okAgent struct{}

func (a *okAgent) RunStream(_ context.Context, _ string, onEvent func(provider.StreamEvent)) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: "all good"})
	return nil
}

func TestIssue4474_CleanStreamStillSucceeds(t *testing.T) {
	out, err := executeTask(context.Background(), &okAgent{}, MailMessage{Content: "hi"}, &Teammate{ID: "tm-2"}, nil, &Team{ID: "team-2"}, 0)
	if err != nil {
		t.Fatalf("clean stream must not error, got %v", err)
	}
	if !strings.Contains(out, "all good") {
		t.Errorf("output should keep streamed text, got %q", out)
	}
	if strings.Contains(out, "[error:") {
		t.Errorf("clean stream output must not contain error markers, got %q", out)
	}
}
