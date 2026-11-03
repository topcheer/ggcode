package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func new3836Engine() *workflowEngine {
	return &workflowEngine{
		steps: map[string]WorkflowStep{
			"gen":   {ID: "gen", Mode: "block", OnCommands: []string{"go generate*"}, ArtifactGlob: "gen.txt"},
			"build": {ID: "build", Mode: "block", OnCommands: []string{"make build*"}, Requires: []string{"gen"}, ArtifactGlob: "bin/app"},
			"test":  {ID: "test", Mode: "block", OnCommands: []string{"go test*"}},
		},
		order:     []string{"gen", "build", "test"},
		loaded:    true,
		startedAt: time.Now(),
	}
}

func mkWFArgs3836(t *testing.T, cmd string) json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func TestIssue3836_StartCommandCheckedAndRecorded(t *testing.T) {
	e := new3836Engine()

	if v := e.checkPreconditions("start_command", mkWFArgs3836(t, "make build")); v == nil {
		t.Fatal("start_command must not bypass block preconditions (#3836 A)")
	} else if v.Missing != "gen" {
		t.Fatalf("violation should name missing step gen, got %q", v.Missing)
	}

	e.completed.Store("gen", struct{}{})
	if v := e.checkPreconditions("start_command", mkWFArgs3836(t, "make build")); v != nil {
		t.Fatalf("grounded prerequisite should pass, got %+v", v)
	}

	e.recordAttempt("start_command", mkWFArgs3836(t, "go generate ./..."), tool.Result{Content: "done"})
	if att := e.recentAttempts("gen", 3); len(att) != 1 {
		t.Fatalf("start_command attempt must enter the ledger, got %d", len(att))
	}
	if !e.stepAttemptedEver("gen") {
		t.Fatal("lifetime count must reflect the start_command attempt")
	}
}

func TestIssue3836_EvictedAttemptAttribution(t *testing.T) {
	e := new3836Engine()

	e.recordAttempt("run_command", mkWFArgs3836(t, "go generate ./..."), tool.Result{Content: "ok"})

	for i := 0; i < wfTraceWindow+5; i++ {
		e.recordAttempt("run_command", mkWFArgs3836(t, "go test ./..."), tool.Result{IsError: true, Content: "boom"})
	}

	if att := e.recentAttempts("gen", 3); len(att) != 0 {
		t.Fatalf("gen entry should have been evicted, got %d", len(att))
	}
	if !e.stepAttemptedEver("gen") {
		t.Fatal("lifetime count must survive window eviction")
	}

	v := e.checkPreconditions("run_command", mkWFArgs3836(t, "make build"))
	if v == nil {
		t.Fatal("expected violation with unmet prerequisite")
	}
	msg := wfAttemptAttribution(v)
	if !strings.Contains(msg, "fell out of the recent trace window") {
		t.Fatalf("evicted step must get downgraded wording, got: %q", msg)
	}
	if strings.Contains(msg, "never executed") {
		t.Fatalf("evicted step must NOT be called never-executed, got: %q", msg)
	}
}

func TestIssue3836_NeverAttemptedWordingKept(t *testing.T) {
	e := new3836Engine()
	v := e.checkPreconditions("run_command", mkWFArgs3836(t, "make build"))
	if v == nil {
		t.Fatal("expected violation with unmet prerequisite")
	}
	msg := wfAttemptAttribution(v)
	if !strings.Contains(msg, "never executed") {
		t.Fatalf("never-attempted step keeps old wording, got: %q", msg)
	}
}
