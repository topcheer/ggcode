package agent

import (
	"encoding/json"
	"testing"
)

func TestDiminishingEditState_NoWarningBeforeMinEdits(t *testing.T) {
	s := newDiminishingEditState()
	// Record 3 edits (below minimum of 4)
	s.recordEdit("edit_file", 500, "a.go")
	s.recordEdit("edit_file", 400, "a.go")
	s.recordEdit("edit_file", 300, "a.go")
	if g := s.check(); g != "" {
		t.Fatalf("expected no warning before %d edits, got: %s", diminishingMinEdits, g)
	}
}

func TestDiminishingEditState_PolishSpiralDetected(t *testing.T) {
	s := newDiminishingEditState()
	// 4 large edits then 4 tiny edits (window=6, keeps last 6)
	s.recordEdit("edit_file", 1000, "a.go")
	s.recordEdit("edit_file", 900, "a.go")
	s.recordEdit("edit_file", 800, "a.go")
	s.recordEdit("edit_file", 100, "a.go") // trivial
	s.recordEdit("edit_file", 50, "a.go")  // trivial
	s.recordEdit("edit_file", 30, "a.go")  // trivial

	g := s.check()
	if g == "" {
		t.Fatal("expected polish-spiral warning, got empty")
	}
	if s.warned != true {
		t.Fatal("expected warned flag to be set")
	}
}

func TestDiminishingEditState_NoWarningWhenConsistent(t *testing.T) {
	s := newDiminishingEditState()
	// All edits roughly the same size -- no decline
	s.recordEdit("edit_file", 500, "a.go")
	s.recordEdit("edit_file", 550, "a.go")
	s.recordEdit("edit_file", 500, "a.go")
	s.recordEdit("edit_file", 520, "a.go")
	s.recordEdit("edit_file", 480, "a.go")
	s.recordEdit("edit_file", 510, "a.go")

	if g := s.check(); g != "" {
		t.Fatalf("expected no warning for consistent edits, got: %s", g)
	}
}

func TestDiminishingEditState_FiresOncePerRun(t *testing.T) {
	s := newDiminishingEditState()
	s.recordEdit("edit_file", 1000, "a.go")
	s.recordEdit("edit_file", 900, "a.go")
	s.recordEdit("edit_file", 800, "a.go")
	s.recordEdit("edit_file", 50, "a.go")
	s.recordEdit("edit_file", 30, "a.go")
	s.recordEdit("edit_file", 20, "a.go")

	g1 := s.check()
	if g1 == "" {
		t.Fatal("expected first warning")
	}
	// Add more trivial edits
	s.recordEdit("edit_file", 10, "a.go")
	s.recordEdit("edit_file", 5, "a.go")
	// Should not fire again
	g2 := s.check()
	if g2 != "" {
		t.Fatalf("expected no second warning, got: %s", g2)
	}
}

func TestDiminishingEditState_ResetClearsState(t *testing.T) {
	s := newDiminishingEditState()
	s.recordEdit("edit_file", 1000, "a.go")
	s.recordEdit("edit_file", 50, "a.go")
	s.warned = true
	s.reset()

	if len(s.entries) != 0 {
		t.Fatalf("expected empty entries after reset, got %d", len(s.entries))
	}
	if s.warned != false {
		t.Fatal("expected warned=false after reset")
	}
}

func TestDiminishingEditState_NoWarningForAllTrivial(t *testing.T) {
	s := newDiminishingEditState()
	// All edits are trivial from the start -- not a polish spiral,
	// just small edits (e.g. fixing typos). avgEarlier < threshold*2.
	s.recordEdit("edit_file", 50, "a.go")
	s.recordEdit("edit_file", 40, "a.go")
	s.recordEdit("edit_file", 30, "a.go")
	s.recordEdit("edit_file", 20, "a.go")

	if g := s.check(); g != "" {
		t.Fatalf("expected no warning when all edits trivial, got: %s", g)
	}
}

func TestMeasureEditSize_EditFile(t *testing.T) {
	args := json.RawMessage(`{"old_text":"hello world","new_text":"hello universe"}`)
	size := measureEditSize("edit_file", args)
	expected := len("hello world") + len("hello universe")
	if size != expected {
		t.Fatalf("expected %d, got %d", expected, size)
	}
}

func TestMeasureEditSize_MultiEditFile(t *testing.T) {
	args := json.RawMessage(`{"edits":[{"old_text":"a","new_text":"b"},{"old_text":"cc","new_text":"ddd"}]}`)
	size := measureEditSize("multi_edit_file", args)
	expected := 1 + 1 + 2 + 3
	if size != expected {
		t.Fatalf("expected %d, got %d", expected, size)
	}
}

func TestMeasureEditSize_WriteFile(t *testing.T) {
	args := json.RawMessage(`{"content":"some content here"}`)
	size := measureEditSize("write_file", args)
	expected := len("some content here")
	if size != expected {
		t.Fatalf("expected %d, got %d", expected, size)
	}
}

func TestMeasureEditSize_EmptyArgs(t *testing.T) {
	if size := measureEditSize("edit_file", nil); size != 0 {
		t.Fatalf("expected 0 for nil args, got %d", size)
	}
	if size := measureEditSize("unknown_tool", json.RawMessage(`{}`)); size != 0 {
		t.Fatalf("expected 0 for unknown tool, got %d", size)
	}
}

func TestMeasureEditSize_BatchReplace(t *testing.T) {
	args := json.RawMessage(`{"files":[{"pattern":"foo","replacement":"bar"}]}`)
	size := measureEditSize("batch_replace", args)
	expected := len("foo") + len("bar")
	if size != expected {
		t.Fatalf("expected %d, got %d", expected, size)
	}
}

func TestAvgEditSize(t *testing.T) {
	entries := []editSizeEntry{
		{size: 100},
		{size: 200},
		{size: 300},
	}
	avg := avgEditSize(entries)
	if avg != 200 {
		t.Fatalf("expected avg 200, got %d", avg)
	}
	if avg := avgEditSize(nil); avg != 0 {
		t.Fatalf("expected 0 for empty, got %d", avg)
	}
}

func TestDiminishingEditState_WindowBounding(t *testing.T) {
	s := newDiminishingEditState()
	// Record more than window size
	for i := 0; i < 12; i++ {
		s.recordEdit("edit_file", 500, "a.go")
	}
	if len(s.entries) > diminishingWindow {
		t.Fatalf("expected at most %d entries, got %d", diminishingWindow, len(s.entries))
	}
}

func TestDiminishingRecordEdit_NilSafe(t *testing.T) {
	a := &Agent{}
	// Should not panic when diminishingEdit is nil
	a.diminishingRecordEdit("edit_file", []byte(`{}`))
}

func TestDiminishingCheck_NilSafe(t *testing.T) {
	a := &Agent{}
	if g := a.diminishingCheck(); g != "" {
		t.Fatalf("expected empty for nil state, got: %s", g)
	}
}

func TestResetDiminishingEdit_NilSafe(t *testing.T) {
	a := &Agent{}
	a.resetDiminishingEdit() // should not panic
}
