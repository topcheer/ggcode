package agent

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

	first := e.outstandingMessageOnce()
	if first == "" || !strings.Contains(first, "gen") {
		t.Fatalf("first call should fire the audit naming step gen, got %q", first)
	}
	if !e.auditFired {
		t.Fatal("latch should be armed after first fire")
	}

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
