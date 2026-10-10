package agent

// #3850 companion: the end-of-run workflow-spec audit must (a) fire at most
// once per engine (latch), and (b) be consumed by a fresh model turn. The
// control-flow `continue` lives in agent.go's stop-gate chain; here we pin
// the latch semantics the gate depends on.

import (
	"strings"
	"testing"
	"time"
)

func TestIssue3850_AuditFiresOnceThenLatches(t *testing.T) {
	e := &workflowEngine{
		steps: map[string]WorkflowStep{
			"gen": {ID: "gen", Mode: "block", OnCommands: []string{"go generate*"}, ArtifactGlob: "gen.txt"},
		},
		order:     []string{"gen"},
		loaded:    true,
		startedAt: time.Now(),
	}

	// First stop attempt: step has no grounded artifact -> message fires.
	first := e.outstandingMessageOnce()
	if first == "" || !strings.Contains(first, "gen") {
		t.Fatalf("first call should fire the audit naming step gen, got %q", first)
	}
	if !e.auditFired {
		t.Fatal("latch should be armed after first fire")
	}

	// Second call on the SAME engine (next stop attempt, same still-missing
	// step) must NOT re-fire - the caller's continue would otherwise inject
	// the reminder every remaining iteration.
	if second := e.outstandingMessageOnce(); second != "" {
		t.Fatalf("latched engine must not re-fire, got %q", second)
	}
}

func TestIssue3850_EmptySpecNeverFires(t *testing.T) {
	e := &workflowEngine{steps: map[string]WorkflowStep{}, order: nil, loaded: true, startedAt: time.Now()}
	if m := e.outstandingMessageOnce(); m != "" {
		t.Fatalf("empty spec must stay inert, got %q", m)
	}
	if e.auditFired {
		t.Fatal("latch must not arm on inert engine")
	}

	// Grounded step: no outstanding, no fire.
	e2 := &workflowEngine{
		steps: map[string]WorkflowStep{
			"gen": {ID: "gen", Mode: "block", OnCommands: []string{"go generate*"}, ArtifactGlob: "gen.txt"},
		},
		order:     []string{"gen"},
		loaded:    true,
		startedAt: time.Now(),
	}
	e2.completed.Store("gen", struct{}{})
	if m := e2.outstandingMessageOnce(); m != "" {
		t.Fatalf("grounded step must not fire audit, got %q", m)
	}
	if e2.auditFired {
		t.Fatal("latch must not arm when nothing is outstanding")
	}
}
