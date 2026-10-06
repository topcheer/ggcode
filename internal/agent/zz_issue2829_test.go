package agent

import "testing"

// #2829: recordToolCall keyword matching was pure substring Contains on tool
// args. "runtime"/"truncate" satisfied a "run" keyword, "author.go" satisfied
// "auth" - the addressed flag burned on unrelated calls and the skip-step
// warning went silent. Hits now require word boundaries (sgKeywordPresent,
// reusing #2745's isWordByte).

func TestIssue2829_SubstringHitDoesNotMarkAddressed(t *testing.T) {
	s := newSubgoalState()
	s.subgoals = []subgoalEntry{
		{number: 1, text: "run the integration tests", keywords: []string{"run"}},
		{number: 2, text: "update auth module", keywords: []string{"auth"}},
	}
	s.recordToolCall("run_command", `{"command":"grep runtime metrics ./src"}`)
	if s.subgoals[0].addressed {
		t.Fatalf(`"runtime" must not satisfy the "run" keyword (#2829)`)
	}
	s.recordToolCall("edit_file", `{"file_path":"src/user/author.go","old_text":"a","new_text":"b"}`)
	if s.subgoals[1].addressed {
		t.Fatalf(`"author.go" must not satisfy the "auth" keyword (#2829)`)
	}
}

func TestIssue2829_TruncateDoesNotSatisfyRun(t *testing.T) {
	s := newSubgoalState()
	s.subgoals = []subgoalEntry{
		{number: 1, text: "run the integration tests", keywords: []string{"run"}},
	}
	s.recordToolCall("run_command", `{"command":"truncate -s 0 /tmp/app.log"}`)
	if s.subgoals[0].addressed {
		t.Fatalf(`"truncate" must not satisfy the "run" keyword (#2829)`)
	}
}

func TestIssue2829_WholeWordHitStillMarksAddressed(t *testing.T) {
	s := newSubgoalState()
	s.subgoals = []subgoalEntry{
		{number: 1, text: "run the integration tests", keywords: []string{"run"}},
		{number: 2, text: "update auth module", keywords: []string{"auth"}},
	}
	s.recordToolCall("run_command", `{"command":"go run ./cmd/server"}`)
	if !s.subgoals[0].addressed {
		t.Fatalf(`"go run" is a whole-word hit for "run" and must mark addressed`)
	}
	s.recordToolCall("edit_file", `{"file_path":"internal/auth/login.go","old_text":"a","new_text":"b"}`)
	if !s.subgoals[1].addressed {
		t.Fatalf(`"internal/auth/" is a whole-word hit for "auth" and must mark addressed`)
	}
}

func TestIssue2829_KeywordAtStringEdges(t *testing.T) {
	s := newSubgoalState()
	s.subgoals = []subgoalEntry{
		{number: 1, text: "run the tests", keywords: []string{"run"}},
	}
	s.recordToolCall("run_command", `{"command":"run"}`)
	if !s.subgoals[0].addressed {
		t.Fatalf(`exact-token args "run" is a whole-word hit at both string edges`)
	}
}

func TestIssue2829_CJKKeywordUnaffected(t *testing.T) {
	s := newSubgoalState()
	s.subgoals = []subgoalEntry{
		{number: 1, text: "更新计划文档", keywords: []string{"计划"}},
	}
	s.recordToolCall("write_file", `{"path":"docs/plan.md","content":"计划已更新"}`)
	if !s.subgoals[0].addressed {
		t.Fatalf("CJK keyword occurrence must stay a whole-word hit (same as Contains)")
	}
}
