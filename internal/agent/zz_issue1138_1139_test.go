package agent

import (
	"testing"
)

func TestIssue1139ReadClassToolsNotVerifiable(t *testing.T) {
	removed := []string{
		"read_file", "grep", "glob", "search_files",
		"lsp_hover", "lsp_definition", "lsp_references",
	}
	for _, tool := range removed {
		if outcomeVerifiableTools[tool] {
			t.Errorf("%s must not be in outcomeVerifiableTools (#1139)", tool)
		}
	}

	kept := []string{"run_command", "start_command", "read_command_output", "lsp_diagnostics"}
	for _, tool := range kept {
		if !outcomeVerifiableTools[tool] {
			t.Errorf("%s should remain in outcomeVerifiableTools", tool)
		}
	}
}

func TestIssue1139ReadingGoSourceNoFalseFailure(t *testing.T) {
	src := "package foo\n\nimport \"fmt\"\n\nfunc Run(x int) error {\n\tif x < 0 {\n\t\treturn fmt.Errorf(\"failed to compute: %d\", x)\n\t}\n\treturn nil\n}\n"

	scenarios := []struct {
		tool string
		body string
	}{
		{"read_file", src},
		{"grep", src + "\nmain.go:4: return fmt.Errorf(...)"},
		{"glob", "handler_a.go\nerror_paths_b.go"},
		{"search_files", "src/a.go:3: errors.Wrap(err)"},
		{"lsp_hover", "func Open(path string) (*os.File, error)"},
		{"lsp_definition", "err variable definition"},
		{"lsp_references", "references of failedFunc"},
	}

	for _, sc := range scenarios {
		s := newOutcomeMisattribState()
		s.recordResult(sc.tool, sc.body, false, 10)
		if s.pendingFailureIter != -1 {
			t.Errorf("%s: external content must not create pending failure (#1139)", sc.tool)
			continue
		}
		hint := s.checkMisattribution("Done, all verified.", 11)
		if hint != "" {
			t.Errorf("%s: unexpected misattribution warning: %s", sc.tool, hint)
		}
	}
}

func TestIssue1139VerifiableToolsStillDetected(t *testing.T) {
	cases := []struct {
		tool    string
		content string
	}{
		{"run_command", "FAIL TestFoo [0.001s]"},
		{"start_command", "./main.go:12: undefined: Bar"},
		{"read_command_output", "exit code 1"},
		{"git_commit", "fatal: unable to auto-detect email"},
		{"lsp_diagnostics", "main.go:12:5: undefined: MissingHelper"},
	}
	for _, tc := range cases {
		s := newOutcomeMisattribState()
		s.recordResult(tc.tool, tc.content, false, 3)
		if s.pendingFailureIter == -1 {
			t.Errorf("%s: failure in command output should still be recorded", tc.tool)
			continue
		}
		if hint := s.checkMisattribution("all done, works correctly", 4); hint == "" {
			t.Errorf("%s: expected misattribution warning after real failure", tc.tool)
		}
	}
}

func TestIssue1139ExplicitErrorFromReadToolStillRecorded(t *testing.T) {

	s := newOutcomeMisattribState()
	s.recordResult("read_file", "permission denied", true, 5)
	if s.pendingFailureIter != 5 {
		t.Fatalf("explicit error should be recorded, got iter=%d", s.pendingFailureIter)
	}
	if hint := s.checkMisattribution("All done and working now.", 6); hint == "" {
		t.Error("expected warning when read tool returned a hard error and agent claims success")
	}
}
