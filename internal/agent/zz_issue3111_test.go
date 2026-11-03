package agent

import (
	"strings"
	"testing"
)

func TestIssue3111DigestIdempotentEmittedGate(t *testing.T) {
	o := newOversightTriageState()
	o.record(tc("edit_file", `{"file_path":"go.mod"}`))

	first := o.digest()
	if !strings.Contains(first, "go.mod") {
		t.Fatalf("first digest must contain the novel go.mod edit, got: %q", first)
	}

	for i := 0; i < 3; i++ {
		if d := o.digest(); d != "" {
			t.Fatalf("digest call %d after emit must be empty (emitted gate), got: %q", i+2, d)
		}
	}
}

func TestIssue3111NovelSurvivesUntilDigestOnAbnormalExit(t *testing.T) {

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

	o.reset()
	if d := o.digest(); d != "" {
		t.Fatalf("digest after reset with no novel decisions must be empty, got: %q", d)
	}
	o.record(tc("edit_file", `{"file_path":"go.mod"}`))
	if d := o.digest(); !strings.Contains(d, "go.mod") {
		t.Fatalf("digest must emit again after reset, got: %q", d)
	}
}
