package agent

import (
	"strings"
	"testing"
)

func TestRecklessExec_DetectorBasics(t *testing.T) {
	t.Run("no edits no warning", func(t *testing.T) {
		s := newRecklessExecState()
		s.recordReadTool("read_file", `{"path":"/tmp/foo.go"}`)
		if s.recordEditTool("edit_file", `{"file_path":"/tmp/bar.go"}`) {
			// bar.go was not read, but only 1 unexplored
			t.Fatal("should not warn for 1 unexplored")
		}
	})

	t.Run("warning when 2+ unexplored edits", func(t *testing.T) {
		s := newRecklessExecState()
		s.iteration = 1
		// Edit file A without reading
		s.recordEditTool("edit_file", `{"file_path":"/tmp/a.go"}`)
		// Edit file B without reading
		if !s.recordEditTool("edit_file", `{"file_path":"/tmp/b.go"}`) {
			t.Fatal("should warn after 2 unexplored edits")
		}
	})

	t.Run("reading before edit prevents warning", func(t *testing.T) {
		s := newRecklessExecState()
		s.iteration = 1
		s.recordReadTool("read_file", `{"path":"/tmp/a.go"}`)
		s.recordReadTool("read_file", `{"path":"/tmp/b.go"}`)
		if s.recordEditTool("edit_file", `{"file_path":"/tmp/a.go"}`) {
			t.Fatal("should not warn for explored file")
		}
		if s.recordEditTool("edit_file", `{"file_path":"/tmp/b.go"}`) {
			t.Fatal("should not warn for explored file")
		}
	})
}

func TestRecklessExec_ExplorationTools(t *testing.T) {
	tools := []string{
		"read_file", "multi_file_read", "search_files", "grep", "glob",
		"list_directory", "code_search", "lsp_definition", "lsp_references",
		"git_diff", "git_show", "git_blame",
	}
	for _, tool := range tools {
		if !recklessIsExplorationTool(tool) {
			t.Errorf("expected %s to be exploration tool", tool)
		}
	}
}

func TestRecklessExec_EditTools(t *testing.T) {
	tools := []string{
		"edit_file", "write_file", "multi_edit_file", "multi_file_write",
		"notebook_edit", "batch_replace",
	}
	for _, tool := range tools {
		if !recklessIsEditTool(tool) {
			t.Errorf("expected %s to be edit tool", tool)
		}
	}
}

func TestRecklessExec_PathExtraction(t *testing.T) {
	t.Run("file_path extraction", func(t *testing.T) {
		paths := recklessExtractPaths("edit_file", `{"file_path":"/tmp/test.go"}`)
		if len(paths) != 1 || paths[0] != "/tmp/test.go" {
			t.Fatalf("expected [/tmp/test.go], got %v", paths)
		}
	})

	t.Run("notebook_path extraction", func(t *testing.T) {
		paths := recklessExtractPaths("notebook_edit", `{"notebook_path":"/tmp/nb.ipynb"}`)
		if len(paths) != 1 || paths[0] != "/tmp/nb.ipynb" {
			t.Fatalf("expected [/tmp/nb.ipynb], got %v", paths)
		}
	})

	t.Run("multi path extraction", func(t *testing.T) {
		args := `{"files":[{"path":"/tmp/a.go"},{"path":"/tmp/b.go"}]}`
		paths := recklessExtractPaths("multi_file_edit", args)
		if len(paths) < 2 {
			t.Fatalf("expected at least 2 paths, got %d: %v", len(paths), paths)
		}
	})
}

func TestRecklessExec_MaxWarnings(t *testing.T) {
	s := newRecklessExecState()
	s.iteration = 1

	// First unexplored edit (no warning yet)
	s.recordEditTool("edit_file", `{"file_path":"/tmp/a.go"}`)
	// Second unexplored edit triggers warning 1
	if !s.recordEditTool("edit_file", `{"file_path":"/tmp/b.go"}`) {
		t.Fatal("expected first warning")
	}

	// Third unexplored edit triggers warning 2
	if !s.recordEditTool("edit_file", `{"file_path":"/tmp/c.go"}`) {
		t.Fatal("expected second warning")
	}

	// Fourth unexplored edit should be suppressed (max 2 warnings)
	if s.recordEditTool("edit_file", `{"file_path":"/tmp/d.go"}`) {
		t.Fatal("should not exceed max warnings")
	}
}

func TestRecklessExec_GraceIterationExceeded(t *testing.T) {
	s := newRecklessExecState()
	s.iteration = recklessGraceIter + 10

	s.recordEditTool("edit_file", `{"file_path":"/tmp/a.go"}`)
	warned := s.recordEditTool("edit_file", `{"file_path":"/tmp/b.go"}`)
	if warned {
		t.Fatal("should not warn after grace iterations")
	}
}

func TestRecklessExec_Reset(t *testing.T) {
	s := newRecklessExecState()
	s.iteration = 1
	s.recordEditTool("edit_file", `{"file_path":"/tmp/a.go"}`)
	s.recordEditTool("edit_file", `{"file_path":"/tmp/b.go"}`)
	s.reset()

	if s.unexplored != 0 || len(s.readFiles) != 0 || len(s.editFiles) != 0 {
		t.Fatal("reset should clear all state")
	}
}

func TestRecklessExec_Warning(t *testing.T) {
	msg := recklessWarning(3)
	if msg == "" {
		t.Fatal("warning text should not be empty")
	}
	if !strings.Contains(msg, "3") {
		t.Fatal("warning should contain count")
	}
	if !strings.Contains(msg, "reckless-exec") {
		t.Fatal("warning should contain tag")
	}
}
