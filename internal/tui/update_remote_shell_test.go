package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/im"
)

// Regression: the TUI-attached IM path (handleRemoteInbound) previously had
// no InboundRouteShell branch, so `$ cmd` messages from IM fell through to
// queuePendingSubmission when a turn was loading - exactly the bug the daemon
// bridge had before 41edc289. This test drives the full remote-inbound path
// with a loading model and asserts the command is NOT queued as a submission.
func TestHandleRemoteInbound_ShellExecutesWhileLoading(t *testing.T) {
	m := newTestModel()
	m.loading = true // agent busy: the old code would queue here

	msg := remoteInboundMsg{Message: im.InboundMessage{Text: "$ printf tuishell-ok"}}
	updated, _ := m.handleRemoteInbound(msg, nil)
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", updated)
	}

	if got := len(m2.pending.items); got != 0 {
		t.Fatalf("shell passthrough must not queue pending submissions while loading, queued=%d", got)
	}
	// The command runs asynchronously via RunInboundShellAsync; output goes
	// to the IM emitter. Execution is fire-and-forget here - the queue-empty
	// assertion above is the contract under test.
}
