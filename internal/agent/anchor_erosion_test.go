package agent

import (
	"encoding/json"
	"testing"
)

func makeAnchorEditArgs(t *testing.T, oldText string) string {
	t.Helper()
	b, err := json.Marshal(map[string]interface{}{
		"file_path": "/test/file.go",
		"old_text":  oldText,
		"new_text":  "replacement",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAnchorErosionReset(t *testing.T) {
	s := newAnchorErosionState()
	s.baselineAnchorLines = 10
	s.baselineCount = 5
	s.recentAnchorLines = []float64{3, 4}
	s.fired = true

	s.reset()

	if s.baselineAnchorLines != 0 || s.baselineCount != 0 || s.fired || len(s.recentAnchorLines) != 0 {
		t.Fatal("reset did not clear all fields")
	}
}

func TestCountAnchorLinesEditFile(t *testing.T) {
	args := makeAnchorEditArgs(t, "line1\nline2\nline3")
	lines := countAnchorLines(args)
	if lines != 3 {
		t.Fatalf("expected 3 lines, got %v", lines)
	}
}

func TestCountAnchorLinesEmpty(t *testing.T) {
	lines := countAnchorLines("not json")
	if lines != 0 {
		t.Fatalf("expected 0 for invalid json, got %v", lines)
	}
}

func TestCountAnchorLinesMultiEdit(t *testing.T) {
	b, _ := json.Marshal(map[string]interface{}{
		"file_path": "/test/file.go",
		"edits": []map[string]interface{}{
			{"old_text": "a\nb\nc", "new_text": "x"},
			{"old_text": "d\ne", "new_text": "y"},
		},
	})
	lines := countAnchorLines(string(b))
	// Multi-edit calls are normalized to the per-edit average so mixed
	// multi-file/single-file workflows stay comparable (#380): (3+2)/2.
	if lines != 2.5 {
		t.Fatalf("expected 2.5 lines (per-edit average) for multi_edit, got %v", lines)
	}
}

func TestCountAnchorLinesNoOldText(t *testing.T) {
	b, _ := json.Marshal(map[string]interface{}{
		"file_path": "/test/file.go",
		"new_text":  "replacement",
	})
	lines := countAnchorLines(string(b))
	if lines != 0 {
		t.Fatalf("expected 0 for no old_text, got %v", lines)
	}
}

func TestRecordEditAnchorIgnoresNonEditTools(t *testing.T) {
	s := newAnchorErosionState()
	hint := s.recordEditAnchor("read_file", `{"path": "/x"}`)
	if hint != "" {
		t.Fatal("should not fire for non-edit tools")
	}
}

func TestRecordEditAnchorNoDecay(t *testing.T) {
	s := newAnchorErosionState()
	// Establish baseline with 10-line edits
	for i := 0; i < anchorErosionMinBaseline; i++ {
		hint := s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "a\nb\nc\nd\ne\nf\ng\nh\ni\nj"))
		if hint != "" {
			t.Fatalf("should not fire during baseline building, iter %d", i)
		}
	}
	// Continue with same-precision edits - should not fire
	for i := 0; i < anchorErosionMinRecent; i++ {
		hint := s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "a\nb\nc\nd\ne\nf\ng\nh\ni\nj"))
		if hint != "" {
			t.Fatalf("should not fire when no decay, iter %d", i)
		}
	}
}

func TestRecordEditAnchorDetectsDecay(t *testing.T) {
	s := newAnchorErosionState()
	// Baseline: 10-line edits (high precision)
	for i := 0; i < anchorErosionMinBaseline; i++ {
		s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "a\nb\nc\nd\ne\nf\ng\nh\ni\nj"))
	}
	// Recent: 1-line edits (severe precision decay)
	for i := 0; i < anchorErosionMinRecent; i++ {
		hint := s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "single_line"))
		if hint != "" {
			// Should fire at some point during recent edits
			if !s.fired {
				t.Fatal("fired flag should be set when hint fires")
			}
			return
		}
	}
	// Should have fired
	if !s.fired {
		t.Fatal("expected decay detection to fire")
	}
}

func TestRecordEditAnchorFiresOnce(t *testing.T) {
	s := newAnchorErosionState()
	for i := 0; i < anchorErosionMinBaseline; i++ {
		s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "a\nb\nc\nd\ne\nf\ng\nh\ni\nj"))
	}
	// Trigger decay
	var firstHint string
	for i := 0; i < anchorErosionMinRecent; i++ {
		hint := s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "x"))
		if hint != "" && firstHint == "" {
			firstHint = hint
		}
	}
	if firstHint == "" {
		t.Fatal("expected at least one hint")
	}
	// Continue - should not fire again
	for i := 0; i < 5; i++ {
		hint := s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "x"))
		if hint != "" {
			t.Fatal("should not fire more than once per run")
		}
	}
}

func TestRecordEditAnchorInsufficientBaseline(t *testing.T) {
	s := newAnchorErosionState()
	// Only 1 edit with low precision - not enough baseline
	hint := s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "x"))
	if hint != "" {
		t.Fatal("should not fire with insufficient baseline")
	}
}

func TestRecordEditAnchorSmallDropNoFire(t *testing.T) {
	s := newAnchorErosionState()
	// Baseline: 5-line edits
	for i := 0; i < anchorErosionMinBaseline; i++ {
		s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "a\nb\nc\nd\ne"))
	}
	// Recent: 4-line edits - drop of only 1 line, below anchorErosionMinDrop
	for i := 0; i < anchorErosionMinRecent+2; i++ {
		hint := s.recordEditAnchor("edit_file", makeAnchorEditArgs(t, "a\nb\nc\nd"))
		if hint != "" {
			t.Fatal("should not fire for small drop (below minimum)")
		}
	}
}

func TestRecordEditAnchorMultiEditFile(t *testing.T) {
	s := newAnchorErosionState()
	b, _ := json.Marshal(map[string]interface{}{
		"file_path": "/test/file.go",
		"edits": []map[string]interface{}{
			{"old_text": "a\nb\nc\nd\ne\nf\ng\nh\ni\nj", "new_text": "x"},
		},
	})
	hint := s.recordEditAnchor("multi_edit_file", string(b))
	if hint != "" {
		t.Fatal("should not fire during baseline")
	}
}

func TestAvgFloat64(t *testing.T) {
	if avgFloat64(nil) != 0 {
		t.Fatal("nil should return 0")
	}
	if avgFloat64([]float64{1, 2, 3}) != 2 {
		t.Fatal("avg of 1,2,3 should be 2")
	}
}

func TestPushRecentEvicts(t *testing.T) {
	s := newAnchorErosionState()
	for i := 0; i < anchorErosionWindow+3; i++ {
		s.pushRecent(float64(i))
	}
	if len(s.recentAnchorLines) != anchorErosionWindow {
		t.Fatalf("expected %d elements, got %d", anchorErosionWindow, len(s.recentAnchorLines))
	}
}
