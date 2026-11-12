package agent

import (
	"strings"
	"testing"
)

func TestIssue4013RunToolNamesAppendedAtExecutionOnly(t *testing.T) {
	// #5543: the tool batch/execution loop moved out of agent.go;
	// beginBatch lives in run_tool_calls.go and the idx loop in
	// run_tool_call_idx.go; scan both in order (shape-agnostic file
	// targeting).
	text, err := readStreamLoopSource("run_tool_calls.go", "run_tool_call_idx.go")
	if err != nil {
		t.Fatalf("read stream-loop source: %v", err)
	}
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
