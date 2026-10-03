package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// r447 negative-signal probes: undo cancels pending observations, repeated
// undos retire promoted user_edit rules, other rules are never touched.

// seedPending simulates one prior positive observation for path.
func seedPending(o *UserEditObserver, path string, mt time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.wrote[path] = mt
	o.pending[path] = &userEditObservation{turns: 1, lastSeen: time.Now()}
}

// TestNegativeSignalCancelsPending: an undo must erase the pending positive
// observation - the "user rewrites this file" reading was a rejection.
func TestNegativeSignalCancelsPending(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-time.Minute)
	seedPending(o, f, mt)

	o.NoteNegativeSignal(f)

	o.mu.Lock()
	_, hasPending := o.pending[f]
	_, hasWrote := o.wrote[f]
	hits := o.negHits[f].turns
	o.mu.Unlock()
	if hasPending || hasWrote {
		t.Fatalf("undo must clear pending+wrote for the file, pending=%v wrote=%v", hasPending, hasWrote)
	}
	if hits != 1 {
		t.Fatalf("expected 1 negHit, got %d", hits)
	}
}

// TestNegativeSignalsRetirePromotedRule: two undos retire a promoted
// user_edit rule (symmetric with userEditPromoteTurns=2).
func TestNegativeSignalsRetirePromotedRule(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "main.go")
	o.store.AddRule(userEditRule(f, 2)) // as-if promoted by r444 path
	// An error-sourced rule must survive the recycling.
	o.store.AddRule(Rule{Category: "build", Rule: "unrelated", ToolPattern: "main.go", Source: ""})
	o.NoteNegativeSignal(f)
	if n := len(o.store.Rules()); n != 2 {
		t.Fatalf("1 signal must not retire yet, rules=%d", n)
	}
	o.NoteNegativeSignal(f)
	rules := o.store.Rules()
	if len(rules) != 1 {
		t.Fatalf("2 signals must retire the user_edit rule, got %d rules", len(rules))
	}
	for _, r := range rules {
		if r.Source == ruleSourceUserEdit {
			t.Fatalf("user_edit rule survived recycling: %+v", r)
		}
	}
	// And the negHit is consumed (a third undo starts a fresh count).
	o.mu.Lock()
	if _, still := o.negHits[f]; still {
		t.Fatal("negHits must be consumed after retirement")
	}
	o.mu.Unlock()
}

// TestNegativeSignalRequiresStore: nil-store observer stays nil-safe.
func TestNegativeSignalRequiresStore(t *testing.T) {
	var o *UserEditObserver
	o.NoteNegativeSignal("/x/y.go") // must not panic
	o2 := newUserEditObserver(nil)
	o2.NoteNegativeSignal("/x/y.go") // nil store: no-op, no panic
}

// TestRemoveUserEditRulesScopesToSourceAndPattern: only user_edit rules
// for the exact basename are removed.
func TestRemoveUserEditRulesScopesToSourceAndPattern(t *testing.T) {
	_, dir := newTestObserver(t)
	rs := NewRuleStore(dir)
	rs.AddRule(userEditRule(filepath.Join(dir, "a.go"), 2))
	rs.AddRule(userEditRule(filepath.Join(dir, "b.go"), 2))
	if n := rs.RemoveUserEditRules("a.go"); n != 1 {
		t.Fatalf("expected 1 removal, got %d", n)
	}
	if n := rs.RemoveUserEditRules("missing.go"); n != 0 {
		t.Fatalf("no-op removal must return 0, got %d", n)
	}
	for _, r := range rs.Rules() {
		if r.ToolPattern == "a\\.go" {
			t.Fatalf("a.go rule survived: %+v", r)
		}
	}
}

// TestNegativeHitsDecayWithTTL: stale negHits decay at turn boundaries
// like pending observations.
func TestNegativeHitsDecayWithTTL(t *testing.T) {
	o, _ := newTestObserver(t)
	f := "/decay/target.go"
	o.mu.Lock()
	o.negHits[f] = &userEditObservation{turns: 1, lastSeen: time.Now().Add(-userEditObsTTL - time.Hour)}
	o.mu.Unlock()
	o.CheckTurnBoundary()
	o.mu.Lock()
	_, still := o.negHits[f]
	o.mu.Unlock()
	if still {
		t.Fatal("stale negHit must decay at turn boundary")
	}
}
