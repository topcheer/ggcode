package agent

// Issue #2758 probe: recordToolCall matched subgoal keywords against the
// TOOL NAME with substring Contains - "read_file" whitewashed a "file"
// keyword, "search_files" whitewashed "search"... any unrelated call
// marked the subgoal addressed and the un-addressed-subgoal warning went
// silent (systematic under-reporting). The fix: keywords may only match in
// the tool ARGUMENTS; tool-name matching is removed entirely.

import "testing"

func TestIssue2758ToolNameSubstringNoLongerWhitewashes(t *testing.T) {
	s := newSubgoalState()
	s.recordAssistantText("Plan:\n1. Modify the config file\n2. Update database schema\n3. Refactor payment service", 1)
	// Same reproduction as the issue: an unrelated read of /tmp/README.md
	// used to mark subgoal 1 ("file") addressed via the tool name
	// "read_file".
	s.recordToolCall("read_file", `{"path": "/tmp/README.md"}`)
	for i, sg := range s.subgoals {
		if sg.addressed {
			t.Fatalf("subgoal %d whitewashed by tool-name substring (kw=%v)", i+1, sg.keywords)
		}
	}
	// And the warning must still fire: neither subgoal was truly addressed.
	if w := s.maybeWarn(10); w == "" {
		t.Fatal("unaddressed-subgoal warning silenced by tool-name whitewash")
	}
}

func TestIssue2758ArgumentSideStillMatches(t *testing.T) {
	s := newSubgoalState()
	s.recordAssistantText("Plan:\n1. Modify the config file\n2. Update database schema\n3. Refactor payment service", 1)
	// A real edit whose arguments name the config file still counts.
	s.recordToolCall("edit_file", `{"file_path": "/app/config.go", "old_text": "x", "new_text": "y"}`)
	if !s.subgoals[0].addressed {
		t.Fatal("argument-side match must keep marking the subgoal addressed")
	}
	if s.subgoals[1].addressed || s.subgoals[2].addressed {
		t.Fatal("subgoals 2/3 must stay unaddressed (nothing touched the schema or payment)")
	}
}
