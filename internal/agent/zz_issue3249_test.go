package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #3249: the r447 negative-signal channel was wired to the agent's own
// undo_edit tool (self-correction) instead of user-initiated undo paths
// (TUI /undo -> checkpoint restore), so agent self-corrections silently
// recycled rules learned from REAL user rewrites, while true user undos
// bumped mtimes that CheckTurnBoundary then misread as POSITIVE signals.
// These tests pin the corrected attribution:
//   - agent self-undo (NoteAgentUndo): wrote[] cleanup only - pending
//     observations and promoted rules must survive, negHits untouched.
//   - user undo (RecordUserUndo -> NoteNegativeSignal): cancels pending,
//     repetition retires rules (already pinned in user_edit_negative_test.go).
//   - source-level wiring pin: agent.go's undo_edit branch must not call
//     NoteNegativeSignal.

// TestIssue3249AgentUndoPreservesPending: agent self-undo must NOT cancel a
// pending observation accumulated from a real user rewrite.
func TestIssue3249AgentUndoPreservesPending(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-time.Minute)
	seedPending(o, f, mt)

	o.NoteAgentUndo(f)

	o.mu.Lock()
	_, hasPending := o.pending[f]
	_, hasWrote := o.wrote[f]
	_, hasNeg := o.negHits[f]
	o.mu.Unlock()
	if !hasPending {
		t.Fatal("agent self-undo must NOT cancel a pending positive observation (that was the #3249 bug)")
	}
	if hasWrote {
		t.Fatal("agent self-undo must drop the tracked write (mtime self-pollution neutralization)")
	}
	if hasNeg {
		t.Fatal("agent self-undo must not create a negHit")
	}
}

// TestIssue3249AgentUndoNeverRetiresRule: any number of agent self-undos
// must never recycle a promoted user_edit rule.
func TestIssue3249AgentUndoNeverRetiresRule(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "main.go")
	o.store.AddRule(userEditRule(f, 2))
	for i := 0; i < 5; i++ {
		o.NoteAgentUndo(f)
	}
	if n := len(o.store.Rules()); n != 1 {
		t.Fatalf("agent self-undos must never retire the rule, rules=%d", n)
	}
	o.mu.Lock()
	_, hasNeg := o.negHits[f]
	o.mu.Unlock()
	if hasNeg {
		t.Fatal("agent self-undo must never feed negHits")
	}
}

// TestIssue3249AgentUndoNeutralizesMtimeBump: end-to-end - the undo's own
// mtime bump must not be misread as a user rewrite at the turn boundary.
// Control arm proves the bump IS detected when the write is still tracked.
func TestIssue3249AgentUndoNeutralizesMtimeBump(t *testing.T) {
	run := func(withUndo bool) int {
		o, dir := newTestObserver(t)
		f := filepath.Join(dir, "main.go")
		if err := os.WriteFile(f, []byte("v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		o.NoteAgentWrite(f)
		if withUndo {
			o.NoteAgentUndo(f)
		}
		// Simulate the undo_edit restore rewriting the file (mtime bump).
		if err := os.WriteFile(f, []byte("v0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		o.CheckTurnBoundary()
		o.mu.Lock()
		obs := o.pending[f]
		o.mu.Unlock()
		if obs == nil {
			return 0
		}
		return obs.turns
	}
	if got := run(true); got != 0 {
		t.Fatalf("agent self-undo's mtime bump must not count as user rewrite, got turns=%d", got)
	}
	if got := run(false); got != 1 {
		t.Fatalf("control arm: un-neutralized external mtime bump must be observed, got turns=%d", got)
	}
}

// TestIssue3249WiringSourcePin: agent.go's undo_edit branch must route to
// NoteAgentUndo, never NoteNegativeSignal (source-level regression pin,
// same discipline as the #1644 scheduler pin).
func TestIssue3249WiringSourcePin(t *testing.T) {
	data, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	i := strings.Index(src, `"undo_edit"`)
	if i < 0 {
		t.Fatal("undo_edit branch not found in agent.go")
	}
	// Window: the branch body extends a few hundred bytes past the match.
	end := i + 700
	if end > len(src) {
		end = len(src)
	}
	window := src[i:end]
	if strings.Contains(window, "NoteNegativeSignal") {
		t.Fatal("undo_edit branch must not call NoteNegativeSignal (#3249: agent self-undo is not a user rejection)")
	}
	if !strings.Contains(window, "NoteAgentUndo") {
		t.Fatal("undo_edit branch must call NoteAgentUndo (mtime self-pollution neutralization)")
	}
}
