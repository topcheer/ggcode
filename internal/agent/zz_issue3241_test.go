package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pendingTurns3241(o *UserEditObserver, path string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if p := o.pending[path]; p != nil {
		return p.turns
	}
	return 0
}

func TestUserEditRatchet_RestampBaselineHidesFormatterWrites(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "foo.go")
	if err := os.WriteFile(f, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.NoteAgentWrite(f)

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(f, future, future); err != nil {
		t.Fatal(err)
	}
	o.RestampBaseline()

	o.CheckTurnBoundary()
	if got := pendingTurns3241(o, f); got != 0 {
		t.Fatalf("formatter write leaked as user edit: turns=%d want 0", got)
	}
}

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

	o.CheckTurnBoundary()
	if got := pendingTurns3241(o, f); got != 1 {
		t.Fatalf("control arm: delta must be observed without restamp, turns=%d want 1", got)
	}
}

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
	o.RestampBaseline()

	user := formatter.Add(2 * time.Second)
	if err := os.Chtimes(f, user, user); err != nil {
		t.Fatal(err)
	}

	o.CheckTurnBoundary()
	if got := pendingTurns3241(o, f); got != 1 {
		t.Fatalf("real user edit after restamp not observed: turns=%d want 1", got)
	}
}

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

	o.RestampBaseline()

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(stays, future, future); err != nil {
		t.Fatal(err)
	}
	o.CheckTurnBoundary()
	if got := pendingTurns3241(o, stays); got != 1 {
		t.Fatalf("surviving file lost tracking after restamp: turns=%d want 1", got)
	}
}
