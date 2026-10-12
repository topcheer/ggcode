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

// #4016: the commit-hint gate comment claimed "This is the last gate" while
// the doc-drift and draft-PR gates below it can still fire - misleading
// maintenance comment, fixed to state the truth.
func TestIssue4016CommitHintCommentIsHonest(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("read agent.go: %v", err)
	}
	text := string(src)
	anchor := "Post-completion commit hint"
	idx := strings.Index(text, anchor)
	if idx < 0 {
		t.Fatal("commit-hint comment anchor not found")
	}
	window := text[idx : idx+700]
	if strings.Contains(window, "This is the last gate") {
		t.Fatal("commit-hint comment must no longer claim 'last gate' (#4016)")
	}
	if !strings.Contains(window, "NOT the last gate") {
		t.Fatal("commit-hint comment must state doc-drift/draft-PR gates may still fire (#4016)")
	}
}
