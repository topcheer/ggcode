package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// pendingTurns3241 reports the observation count recorded for path (0 if none).
func pendingTurns3241(o *UserEditObserver, path string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if p := o.pending[path]; p != nil {
		return p.turns
	}
	return 0
}

// #3241: the agent's own shell formatter (gofmt -w via run_command) moved
// mtimes of files the ratchet already baselined; the next turn boundary
// read that delta as a manual user edit and, after userEditPromoteTurns
// rounds, promoted a false user-preference rule. Production ordering per
// turn: NoteAgentWrite... -> run_command gofmt (mtime moves) ->
// RestampBaseline (from the #750 hook) -> [next user message]
// CheckTurnBoundary. One boundary check per scenario, as in production.
func TestUserEditRatchet_RestampBaselineHidesFormatterWrites(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "foo.go")
	if err := os.WriteFile(f, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.NoteAgentWrite(f) // baseline sampled from the real file mtime

	// Agent's own formatter write: mtime moves, no user involvement.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(f, future, future); err != nil {
		t.Fatal(err)
	}
	o.RestampBaseline() // #750 hook fires after shellMutatesSources

	o.CheckTurnBoundary()
	if got := pendingTurns3241(o, f); got != 0 {
		t.Fatalf("formatter write leaked as user edit: turns=%d want 0", got)
	}
}

// Without the restamp the same sequence MUST observe (control arm: proves
// the first test passes because of RestampBaseline, not because the
// mtime delta was unobservable).
func TestUserEditRatchet_FormatterWriteObservedWithoutRestamp(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "bar.go")
	if err := os.WriteFile(f, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.NoteAgentWrite(f)

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(f, future, future); err != nil {
		t.Fatal(err)
	}
	// NO RestampBaseline: original #3241 behavior.

	o.CheckTurnBoundary()
	if got := pendingTurns3241(o, f); got != 1 {
		t.Fatalf("control arm: delta must be observed without restamp, turns=%d want 1", got)
	}
}

// A real user edit after the restamp must still be observed: restamp
// baselines T2, the user then rewrites the file (T3) before the next
// boundary.
func TestUserEditRatchet_RealEditAfterRestampStillObserved(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "baz.go")
	if err := os.WriteFile(f, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.NoteAgentWrite(f)

	formatter := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(f, formatter, formatter); err != nil {
		t.Fatal(err)
	}
	o.RestampBaseline() // baseline now formatter-time

	user := formatter.Add(2 * time.Second) // user edit AFTER the restamp
	if err := os.Chtimes(f, user, user); err != nil {
		t.Fatal(err)
	}

	o.CheckTurnBoundary()
	if got := pendingTurns3241(o, f); got != 1 {
		t.Fatalf("real user edit after restamp not observed: turns=%d want 1", got)
	}
}

// RestampBaseline must tolerate a tracked file deleted between the write
// and the restamp (mtimeOf fails): keep the stale baseline rather than
// crashing or clearing tracking for surviving entries.
func TestUserEditRatchet_RestampBaselineSkipsDeletedFiles(t *testing.T) {
	o, dir := newTestObserver(t)
	gone := filepath.Join(dir, "gone.go")
	stays := filepath.Join(dir, "stays.go")
	for _, p := range []string{gone, stays} {
		if err := os.WriteFile(p, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o.NoteAgentWrite(gone)
	o.NoteAgentWrite(stays)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	o.RestampBaseline() // must not panic; stays must remain tracked

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(stays, future, future); err != nil {
		t.Fatal(err)
	}
	o.CheckTurnBoundary()
	if got := pendingTurns3241(o, stays); got != 1 {
		t.Fatalf("surviving file lost tracking after restamp: turns=%d want 1", got)
	}
}
