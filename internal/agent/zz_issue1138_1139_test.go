package agent

import (
	"strings"
	"testing"
)

func TestIssue1138SelfEditNeverIntegrates(t *testing.T) {
	o := newOrphanFileState()

	o.recordToolCall("write_file", `{"path":"/tmp/proj/newmod/util.go"}`, 1)

	sawWarning := false
	for i := 0; i < 5; i++ {
		args := `{"file_path":"/tmp/proj/newmod/util.go","old_text":"a","new_text":"b"}`
		if w := o.recordToolCall("edit_file", args, i+2); w != "" {
			sawWarning = true
			if !strings.Contains(w, "Orphaned File") {
				t.Fatalf("warning text mismatch: %s", w)
			}
			break
		}
	}

	if o.integrated {
		t.Fatal("self-edits must not mark state as integrated")
	}
	if len(o.newFiles) != 1 {
		t.Fatalf("new file should remain tracked after self-edits, got %d", len(o.newFiles))
	}
	if !sawWarning {
		t.Fatal("orphan detection should fire when agent only polishes the new file itself")
	}
}

func TestIssue1138EditOtherExistingFileIntegrates(t *testing.T) {
	o := newOrphanFileState()

	o.recordToolCall("write_file", `{"path":"helper.go"}`, 1)

	warn := o.recordToolCall("edit_file", `{"file_path":"main.go"}`, 2)
	if warn != "" {
		t.Fatalf("edit to existing other file should integrate, got: %s", warn)
	}
	if len(o.newFiles) != 0 {
		t.Fatalf("tracking should be cleared after real integration, got %d files", len(o.newFiles))
	}
}

func TestIssue1138MultiEditMixedTargetsIntegrates(t *testing.T) {
	o := newOrphanFileState()
	o.recordToolCall("write_file", `{"path":"feature.go"}`, 1)

	args := `{"files":[{"file_path":"feature.go"},{"file_path":"main.go"}]}`
	if warn := o.recordToolCall("multi_file_edit", args, 2); warn != "" {
		t.Fatalf("mixed-target batch edit should integrate, got: %s", warn)
	}
	if o.integrated {

	}
}

func TestIssue1138MultiEditAllNewFilesNotIntegrated(t *testing.T) {
	o := newOrphanFileState()
	o.recordToolCall("write_file", `{"path":"a.go"}`, 1)
	o.recordToolCall("write_file", `{"path":"b.go"}`, 2)

	args := `{"files":[{"path":"a.go"},{"path":"b.go"}]}`
	warn := o.recordToolCall("batch_replace", args, 3)
	if warn != "" && o.callsSince < orphanCallThreshold {
		t.Fatalf("premature warning before threshold: %s", warn)
	}
	if o.integrated {
		t.Fatal("batch edit over only-new files must not mark integrated")
	}
}

func TestIssue1138RelativeAbsoluteSelfEdit(t *testing.T) {
	o := newOrphanFileState()
	o.recordToolCall("write_file", `{"path":"src/newpkg/engine.go"}`, 1)

	warn := o.recordToolCall("edit_file", `{"file_path":"/abs/root/src/newpkg/engine.go"}`, 2)
	if warn != "" {
		t.Fatalf("self-edit via alternate path form should stay un-integrated, got: %s", warn)
	}
	if o.integrated {
		t.Fatal("base-name match failed: treated self-edit as integration")
	}
}

func TestIssue1138UnparseableArgsFallback(t *testing.T) {
	o := newOrphanFileState()
	o.recordToolCall("write_file", `{"path":"f.go"}`, 1)

	warn := o.recordToolCall("edit_file", `{}`, 2)
	if warn != "" {
		t.Fatalf("unparseable args should keep legacy integration fallback, got: %s", warn)
	}
}

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
