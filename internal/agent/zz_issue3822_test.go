package agent

// #3822 companion: a requires entry referencing a nonexistent step ID
// (typo) must surface as an explicit "does not exist" attribution rather
// than the generic "never attempted" dead end - in block mode the guarded
// command would otherwise be rejected forever with no repair hint.

import (
	"strings"
	"testing"
)

func TestIssue3822_UnknownRequiresAttribution(t *testing.T) {
	e := newWFEngineWithSpec(t, `{"steps":[
		{"id":"test","mode":"block","on_commands":["git push*"],"requires":["tests"],"artifact_glob":"ok.txt"}
	]}`)
	v := e.checkPreconditions(runGitPush.Name, runGitPush.Arguments) // lazily loads the spec
	if v == nil {
		t.Fatalf("block mode must still reject (unknown req is unsatisfiable)")
	}
	if got := e.unknownReq["test"]; len(got) != 1 || got[0] != "tests" {
		t.Fatalf("merge must record unknown requires entry, got %v", got)
	}
	if !v.UnknownRequires || v.Missing != "tests" {
		t.Fatalf("violation must flag unknown requires, got %+v", v)
	}
	attr := wfAttemptAttribution(v)
	if !strings.Contains(attr, `step "tests" does not exist`) {
		t.Fatalf("attribution must name the unknown step ID:\n%s", attr)
	}
	if strings.Contains(attr, "never executed") {
		t.Fatalf("generic never-attempted attribution must be replaced:\n%s", attr)
	}
}

func TestIssue3822_KnownRequiresUnaffected(t *testing.T) {
	e := newWFEngineWithSpec(t, `{"steps":[
		{"id":"build","mode":"block","on_commands":["make"],"artifact_glob":"bin/app"},
		{"id":"deploy","mode":"block","on_commands":["git push*"],"requires":["build"]}
	]}`)
	if len(e.unknownReq) != 0 {
		t.Fatalf("valid spec must record no unknown requires, got %v", e.unknownReq)
	}
	v := e.checkPreconditions(runGitPush.Name, runGitPush.Arguments)
	if v == nil {
		t.Fatalf("unmet-but-known req must still violate")
	}
	if v.UnknownRequires {
		t.Fatalf("known req must not set UnknownRequires")
	}
	attr := wfAttemptAttribution(v)
	if !strings.Contains(attr, "never executed") {
		t.Fatalf("known-req attribution must stay generic (attempt-based):\n%s", attr)
	}
}
