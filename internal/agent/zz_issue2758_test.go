package agent

import "testing"

func TestIssue2758ToolNameSubstringNoLongerWhitewashes(t *testing.T) {
	s := newSubgoalState()
	s.recordAssistantText("Plan:\n1. Modify the config file\n2. Update database schema\n3. Refactor payment service", 1)

	s.recordToolCall("read_file", `{"path": "/tmp/README.md"}`)
	for i, sg := range s.subgoals {
		if sg.addressed {
			t.Fatalf("subgoal %d whitewashed by tool-name substring (kw=%v)", i+1, sg.keywords)
		}
	}

	if w := s.maybeWarn(10); w == "" {
		t.Fatal("unaddressed-subgoal warning silenced by tool-name whitewash")
	}
}

func TestIssue2758ArgumentSideStillMatches(t *testing.T) {
	s := newSubgoalState()
	s.recordAssistantText("Plan:\n1. Modify the config file\n2. Update database schema\n3. Refactor payment service", 1)

	s.recordToolCall("edit_file", `{"file_path": "/app/config.go", "old_text": "x", "new_text": "y"}`)
	if !s.subgoals[0].addressed {
		t.Fatal("argument-side match must keep marking the subgoal addressed")
	}
	if s.subgoals[1].addressed || s.subgoals[2].addressed {
		t.Fatal("subgoals 2/3 must stay unaddressed (nothing touched the schema or payment)")
	}
}
