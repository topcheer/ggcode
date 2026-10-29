package agent

import (
	"os"
	"strings"
	"testing"
)

// #4013: runToolNames must be appended only when a result is actually
// produced, never in the planning loop (dedup/ask_user/redaction skips used
// to leave phantom names, violating the agent_state_fields contract
// "appended only as tools execute during a run").
func TestIssue4013RunToolNamesAppendedAtExecutionOnly(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)
	begin := strings.Index(text, "a.cascade.beginBatch()")
	execLoop := strings.Index(text, "for idx, tc := range toolCalls")
	if begin < 0 || execLoop <= begin {
		t.Fatal("tool batch / execution loop anchors not found")
	}
	if strings.Contains(text[begin:execLoop], "runToolNames = append") {
		t.Fatal("planning loop must not append runToolNames (#4013)")
	}
	redactGate := strings.Index(text, "checkRedactedInWrite(tc.Name")
	if redactGate < 0 {
		t.Fatal("redaction gate anchor not found")
	}
	resultDecl := strings.Index(text[redactGate:], "var result tool.Result") + redactGate
	if resultDecl < redactGate {
		t.Fatal("result declaration anchor not found")
	}
	if !strings.Contains(text[redactGate:resultDecl+400], "runToolNames = append") {
		t.Fatal("execution loop must append runToolNames past the skip gates (#4013)")
	}
}
