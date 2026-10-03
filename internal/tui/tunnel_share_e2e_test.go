package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/tool"
)

// TestAgentShareRequestRoundTrip drives the full synchronous bridge the
// start_share/stop_share tools rely on: adapter.send -> Update dispatch ->
// handleAgentShareRequest -> reply channel. The send func stands in for
// sendTUI by invoking the same handler Update would (single-goroutine test,
// reply buffered, no deadlock: handler replies before the blocking read).
func TestAgentShareRequestRoundTrip(t *testing.T) {
	m := NewModel(nil, nil)
	send := func(msg tea.Msg) {
		if req, ok := msg.(agentShareRequestMsg); ok {
			m.handleAgentShareRequest(req)
		}
	}
	adapter := tunnelShareControlAdapter{m: &m, send: send}

	// a) start with no tunnel host: must surface the HOST guard error - NOT
	// "no running UI program" (send==nil) and NOT a registry/already-registered
	// failure (those happen before this layer and would return a different error).
	_, err := adapter.StartAgentShare()
	if err == nil {
		t.Fatal("expected error with nil tunnel host")
	}
	if !strings.Contains(err.Error(), "tunnel host not initialized") {
		t.Fatalf("expected tunnel-host guard error, got: %v", err)
	}

	// stop with no session: no-op success, fast reply (no timeout path).
	if err := adapter.StopAgentShare(); err != nil {
		t.Fatalf("inactive stop must succeed, got: %v", err)
	}
}

// TestAgentShareRequestNoSendGuard pins the availability gate: adapter with
// no send wiring answers errShareNoProgram instead of blocking to timeout.
func TestAgentShareRequestNoSendGuard(t *testing.T) {
	adapter := tunnelShareControlAdapter{m: &Model{}}
	done := make(chan error, 1)
	go func() { done <- adapter.StopAgentShare() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "no running UI program") {
			t.Fatalf("expected no-program error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("guard missing: adapter blocked instead of failing fast")
	}
}

// TestAgentShareStartRelaysFullURL asserts the URL relay chain end to end at
// the tool layer: whatever the Update loop puts into agentShareReply.connectURL
// must reach the agent VERBATIM (scheme+host+path+token), with the verbatim
// instruction present.
func TestAgentShareStartRelaysFullURL(t *testing.T) {
	full := "https://relay.example/join?room=AB12CD34&token=tok-9f8e7d"
	send := func(msg tea.Msg) {
		req, ok := msg.(agentShareRequestMsg)
		if !ok || req.stop {
			return
		}
		req.reply <- agentShareReply{connectURL: full}
	}
	adapter := tunnelShareControlAdapter{m: &Model{}, send: send}
	st := tool.StartShareTool{Controller: adapter}
	res, err := st.Execute(t.Context(), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, full) {
		t.Fatalf("full connect URL not relayed verbatim:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "VERBATIM") {
		t.Fatalf("verbatim instruction missing:\n%s", res.Content)
	}
}
