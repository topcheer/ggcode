package agent

import (
	"strings"
	"testing"
)

// #3111: the novel-decision digest must be emittable on ANY run exit
// (error / cancel / iteration limit), not just natural convergence. The
// fix registers a defer next to oversightTriage.reset(); its safety rests
// on digest() being idempotent via the emitted gate - these tests pin
// that contract so the defer can never double-emit.

func TestIssue3111DigestIdempotentEmittedGate(t *testing.T) {
	o := newOversightTriageState()
	o.record(tc("edit_file", `{"file_path":"go.mod"}`))

	first := o.digest()
	if !strings.Contains(first, "go.mod") {
		t.Fatalf("first digest must contain the novel go.mod edit, got: %q", first)
	}
	// The convergence-path emit (or the defer) sets the gate: every later
	// call must return "" so the exit defer is a no-op on runs that
	// already emitted.
	for i := 0; i < 3; i++ {
		if d := o.digest(); d != "" {
			t.Fatalf("digest call %d after emit must be empty (emitted gate), got: %q", i+2, d)
		}
	}
}

func TestIssue3111NovelSurvivesUntilDigestOnAbnormalExit(t *testing.T) {
	// Simulate the abnormal-exit ordering: novel decisions recorded, NO
	// digest emitted on the way in (as on error/cancel paths), then the
	// run-return defer fires digest once - it must surface the decisions.
	o := newOversightTriageState()
	o.record(tc("edit_file", `{"file_path":"go.sum"}`))
	o.record(tc("git_push", `{"remote":"origin"}`))

	d := o.digest()
	if !strings.Contains(d, "go.sum") {
		t.Fatalf("defer-time digest must surface the supply-chain novel edit, got: %q", d)
	}
	if !strings.Contains(d, "git_push") {
		t.Fatalf("defer-time digest must surface the irreversible git push, got: %q", d)
	}
}

func TestIssue3111ResetClearsEmittedGateForNextRun(t *testing.T) {
	o := newOversightTriageState()
	o.record(tc("edit_file", `{"file_path":"go.mod"}`))
	if d := o.digest(); d == "" {
		t.Fatal("first run digest must be non-empty")
	}
	// New user turn: reset() must clear both the novel list and the
	// emitted gate so the NEXT run (and its exit defer) can emit again.
	o.reset()
	if d := o.digest(); d != "" {
		t.Fatalf("digest after reset with no novel decisions must be empty, got: %q", d)
	}
	o.record(tc("edit_file", `{"file_path":"go.mod"}`))
	if d := o.digest(); !strings.Contains(d, "go.mod") {
		t.Fatalf("digest must emit again after reset, got: %q", d)
	}
}
