package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func TestIssue3249RecordUserUndoDelegates(t *testing.T) {
	o, dir := newTestObserver(t)
	a := &Agent{}
	a.mu.Lock()
	a.userEditObs = o
	a.mu.Unlock()
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedPending(o, f, time.Now().Add(-time.Minute))

	a.RecordUserUndo(f)

	o.mu.Lock()
	_, hasPending := o.pending[f]
	_, hasWrote := o.wrote[f]
	hits := 0
	if o.negHits[f] != nil {
		hits = o.negHits[f].turns
	}
	o.mu.Unlock()
	if hasPending || hasWrote {
		t.Fatalf("user undo must clear pending+wrote, pending=%v wrote=%v", hasPending, hasWrote)
	}
	if hits != 1 {
		t.Fatalf("user undo must feed the negative channel, negHits=%d", hits)
	}
}

func TestIssue3249WiringSourcePin(t *testing.T) {
	data, err := readAgentAndProcessingSources()
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	i := strings.Index(src, `"undo_edit"`)
	if i < 0 {
		t.Fatal("undo_edit branch not found in agent.go")
	}

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
