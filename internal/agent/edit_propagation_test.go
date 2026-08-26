package agent

import "testing"

func TestEditPropagation_BasicTracking(t *testing.T) {
	s := newEditPropagationState()

	// Editing the same file 3 times should count as 1 distinct file
	s.recordEdit("edit_file", `{"file_path":"/foo/bar.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/foo/bar.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/foo/bar.go"}`)

	if len(s.distinctFiles) != 1 {
		t.Errorf("expected 1 distinct file, got %d", len(s.distinctFiles))
	}
}

func TestEditPropagation_MultipleDistinctFiles(t *testing.T) {
	s := newEditPropagationState()

	s.recordEdit("edit_file", `{"file_path":"/foo/a.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/foo/b.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/foo/c.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/foo/d.go"}`)

	if len(s.distinctFiles) != 4 {
		t.Errorf("expected 4 distinct files, got %d", len(s.distinctFiles))
	}
}

func TestEditPropagation_WarnAt4Files(t *testing.T) {
	s := newEditPropagationState()

	s.recordEdit("edit_file", `{"file_path":"/a.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/b.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/c.go"}`)

	// 3 files: no warning yet
	msg := s.maybeWarn(5)
	if msg != "" {
		t.Error("should not warn at 3 files")
	}

	s.recordEdit("edit_file", `{"file_path":"/d.go"}`)

	// 4 files: first warning
	msg = s.maybeWarn(5)
	if msg == "" {
		t.Error("should warn at 4 files")
	}
}

func TestEditPropagation_EscalateAt7(t *testing.T) {
	s := newEditPropagationState()

	for i, p := range []string{"/a.go", "/b.go", "/c.go", "/d.go", "/e.go", "/f.go", "/g.go"} {
		_ = i
		s.recordEdit("edit_file", `{"file_path":"`+p+`"}`)
	}

	msg := s.maybeWarn(5)
	if msg == "" {
		t.Fatal("should warn at 7 files")
	}
}

func TestEditPropagation_GreenBuildResets(t *testing.T) {
	s := newEditPropagationState()

	s.recordEdit("edit_file", `{"file_path":"/a.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/b.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/c.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/d.go"}`)

	if len(s.distinctFiles) != 4 {
		t.Fatalf("expected 4 distinct files, got %d", len(s.distinctFiles))
	}

	s.recordGreenBuild()

	if len(s.distinctFiles) != 0 {
		t.Errorf("expected 0 files after green build, got %d", len(s.distinctFiles))
	}
}

func TestEditPropagation_MaxWarnings(t *testing.T) {
	s := newEditPropagationState()

	s.recordEdit("edit_file", `{"file_path":"/a.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/b.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/c.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/d.go"}`)

	// First warning fires
	msg1 := s.maybeWarn(5)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second call: suppressed (1 per run, batch 2 guidance-noise cleanup)
	s.recordEdit("edit_file", `{"file_path":"/e.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/f.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/g.go"}`)

	msg2 := s.maybeWarn(5)
	if msg2 != "" {
		t.Fatalf("expected second warning to be suppressed, got: %s", msg2)
	}

	// Third call: should be suppressed
	msg3 := s.maybeWarn(5)
	if msg3 != "" {
		t.Error("should not warn more than max times")
	}
}

func TestEditPropagation_Reset(t *testing.T) {
	s := newEditPropagationState()

	s.recordEdit("edit_file", `{"file_path":"/a.go"}`)
	s.recordEdit("edit_file", `{"file_path":"/b.go"}`)

	s.reset()

	if len(s.distinctFiles) != 0 {
		t.Errorf("expected 0 files after reset, got %d", len(s.distinctFiles))
	}
	if s.warningsIssued != 0 {
		t.Errorf("expected 0 warnings after reset, got %d", s.warningsIssued)
	}
}

func TestEditPropagation_MultiFileEdit(t *testing.T) {
	s := newEditPropagationState()

	// multi_file_edit with files array containing multiple paths
	s.recordEdit("multi_file_edit", `{"files":[{"path":"/a.go"},{"path":"/b.go"}]}`)

	if len(s.distinctFiles) != 2 {
		t.Errorf("expected 2 distinct files from multi_file_edit, got %d", len(s.distinctFiles))
	}
}

func TestEditPropagation_NonEditToolIgnored(t *testing.T) {
	s := newEditPropagationState()

	// read_file is not an editing tool, should be ignored
	s.recordEdit("read_file", `{"file_path":"/a.go"}`)

	if len(s.distinctFiles) != 0 {
		t.Errorf("expected 0 files from non-edit tool, got %d", len(s.distinctFiles))
	}
}
