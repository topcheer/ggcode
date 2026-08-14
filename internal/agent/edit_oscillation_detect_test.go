package agent

import (
	"encoding/json"
	"testing"
)

func makeEditArgs(t *testing.T, path, oldText, newText string) json.RawMessage {
	t.Helper()
	args := map[string]any{
		"path":     path,
		"old_text": oldText,
		"new_text": newText,
	}
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOscillationReset(t *testing.T) {
	o := newOscillationState()
	o.addSignature("foo.go", "old", "new", 1)
	o.fired = 1
	o.reset()
	if len(o.signatures) != 0 {
		t.Fatalf("expected empty signatures after reset, got %d", len(o.signatures))
	}
	if o.fired != 0 {
		t.Fatalf("expected fired=0 after reset, got %d", o.fired)
	}
}

func TestOscillationNoReversal(t *testing.T) {
	o := newOscillationState()
	// Edit file once - no reversal possible
	o.addSignature("foo.go", "oldA", "newA", 1)
	if msg := o.check(); msg != "" {
		t.Fatalf("expected no warning with single edit, got: %s", msg)
	}
}

func TestOscillationSimpleProgression(t *testing.T) {
	o := newOscillationState()
	// Two different edits to the same file, both progressing forward
	o.addSignature("foo.go", "oldA", "newA", 1)
	o.addSignature("foo.go", "newA", "newB", 2)
	// newA appears as new_text then as old_text - that's a normal progression,
	// not a reversal, because the reversal detector counts when new_text matches
	// a PRIOR old_text (re-adding something removed) or old_text matches a PRIOR
	// new_text (removing something added). Here newA was new_text, then became
	// old_text. That IS counted as 1 reversal. But threshold is 2, so no warning.
	if msg := o.check(); msg != "" {
		t.Fatalf("expected no warning below threshold, got: %s", msg)
	}
}

func TestOscillationDetectsBackAndForth(t *testing.T) {
	o := newOscillationState()
	// Edit 1: change A -> B
	o.addSignature("foo.go", "contentA", "contentB", 1)
	// Edit 2: change B -> A (revert)
	o.addSignature("foo.go", "contentB", "contentA", 2)
	// Edit 3: change A -> B again (re-apply)
	o.addSignature("foo.go", "contentA", "contentB", 3)

	msg := o.check()
	if msg == "" {
		t.Fatal("expected oscillation warning for A->B->A->B pattern")
	}
}

func TestOscillationMaxFiredOnce(t *testing.T) {
	o := newOscillationState()
	o.addSignature("foo.go", "A", "B", 1)
	o.addSignature("foo.go", "B", "A", 2)
	o.addSignature("foo.go", "A", "B", 3)

	first := o.check()
	if first == "" {
		t.Fatal("expected first warning")
	}
	second := o.check()
	if second != "" {
		t.Fatal("expected no second warning after firing once")
	}
}

func TestOscillationContentSigDeterministic(t *testing.T) {
	sig1 := contentSig("hello world")
	sig2 := contentSig("hello world")
	sig3 := contentSig("hello earth")
	if sig1 != sig2 {
		t.Fatal("same content should produce same signature")
	}
	if sig1 == sig3 {
		t.Fatal("different content should produce different signature")
	}
}

func TestOscillationIdenticalOldNewSkipped(t *testing.T) {
	o := newOscillationState()
	o.addSignature("foo.go", "same", "same", 1)
	if len(o.signatures["foo.go"]) != 0 {
		t.Fatal("expected no signatures when old_text == new_text")
	}
}

func TestOscillationMultipleFiles(t *testing.T) {
	o := newOscillationState()
	// File 1: oscillating
	o.addSignature("a.go", "A", "B", 1)
	o.addSignature("a.go", "B", "A", 2)
	o.addSignature("a.go", "A", "B", 3)
	// File 2: not oscillating
	o.addSignature("b.go", "X", "Y", 1)

	msg := o.check()
	if msg == "" {
		t.Fatal("expected warning with one oscillating file")
	}
}

func TestOscillationRecordEditFromToolCall(t *testing.T) {
	o := newOscillationState()
	args := makeEditArgs(t, "test.go", "oldContent", "newContent")
	o.recordEdit("edit_file", args, 1)
	if len(o.signatures["test.go"]) != 2 {
		t.Fatalf("expected 2 signatures (old+new), got %d", len(o.signatures["test.go"]))
	}
}

func TestOscillationRecordEditMultiEdit(t *testing.T) {
	o := newOscillationState()
	args := map[string]any{
		"path": "multi.go",
		"edits": []any{
			map[string]any{"old_text": "old1", "new_text": "new1"},
			map[string]any{"old_text": "old2", "new_text": "new2"},
		},
	}
	b, _ := json.Marshal(args)
	o.recordEdit("multi_edit_file", b, 1)
	if len(o.signatures["multi.go"]) != 2 {
		t.Fatalf("expected 2 signatures for multi_edit (combined old+new), got %d", len(o.signatures["multi.go"]))
	}
}

func TestOscillationMaxTracked(t *testing.T) {
	o := newOscillationState()
	// Fill up to max tracked
	for i := 0; i < oscillationMaxTracked; i++ {
		o.addSignature("file"+string(rune('a'+i))+".go", "old", "new", 1)
	}
	// Try to add a new file beyond capacity
	o.addSignature("overflow.go", "old", "new", 1)
	if _, exists := o.signatures["overflow.go"]; exists {
		t.Fatal("should not track new files beyond max")
	}
}

// ---------------------------------------------------------------------------
// Issue #308 regression tests: false positives from empty-side signatures and
// from count-only (direction-unaware) reversal detection.
// ---------------------------------------------------------------------------

func TestOscillationRepeatedDeletionsNoFalsePositive(t *testing.T) {
	// Four normal line deletions: each is a pure deletion (new_text empty).
	// Previously all deletions shared the hash("") signature, so 4 deletions
	// counted as reversals=2 and fired a false warning (issue #308 defect A).
	o := newOscillationState()
	o.addSignature("foo.go", "line1\n", "", 1)
	o.addSignature("foo.go", "line2\n", "", 2)
	o.addSignature("foo.go", "line3\n", "", 3)
	o.addSignature("foo.go", "line4\n", "", 4)
	if msg := o.check(); msg != "" {
		t.Fatalf("expected no warning for 4 normal deletions, got: %s", msg)
	}
	// Each deletion records only its non-empty old_text signature (the empty
	// new_text side is skipped — it used to record the shared hash("") sig).
	if len(o.signatures["foo.go"]) != 4 {
		t.Fatalf("expected 4 old-side signatures for pure deletions, got %d", len(o.signatures["foo.go"]))
	}
	for _, s := range o.signatures["foo.go"] {
		if s.isNew {
			t.Fatal("pure deletions must not record new_text signatures")
		}
	}
}

func TestOscillationRepeatedInsertionsNoFalsePositive(t *testing.T) {
	// Four normal insertions: each is a pure insertion (old_text empty).
	o := newOscillationState()
	o.addSignature("foo.go", "", "line1\n", 1)
	o.addSignature("foo.go", "", "line2\n", 2)
	o.addSignature("foo.go", "", "line3\n", 3)
	o.addSignature("foo.go", "", "line4\n", 4)
	if msg := o.check(); msg != "" {
		t.Fatalf("expected no warning for 4 normal insertions, got: %s", msg)
	}
	if len(o.signatures["foo.go"]) != 4 {
		t.Fatalf("expected 4 new-side signatures for pure insertions, got %d", len(o.signatures["foo.go"]))
	}
	for _, s := range o.signatures["foo.go"] {
		if !s.isNew {
			t.Fatal("pure insertions must not record old_text signatures")
		}
	}
}

func TestOscillationBatchReplaceRepeatedNoFalsePositive(t *testing.T) {
	// Fixing a repeated pattern with identical arguments three times (e.g.
	// renaming a symbol in multiple spots with the same old->new pair) is
	// forward progress, not oscillation (issue #308 defect B).
	o := newOscillationState()
	o.addSignature("foo.go", "oldName", "newName", 1)
	o.addSignature("foo.go", "oldName", "newName", 2)
	o.addSignature("foo.go", "oldName", "newName", 3)
	if msg := o.check(); msg != "" {
		t.Fatalf("expected no warning for repeated identical replacements, got: %s", msg)
	}
}

func TestOscillationChainedProgressionNoFalsePositive(t *testing.T) {
	// Progressive chained edits A->B->C->D: each edit's old_text is the prior
	// edit's new_text. This is forward progress, not a reversal.
	o := newOscillationState()
	o.addSignature("foo.go", "A", "B", 1)
	o.addSignature("foo.go", "B", "C", 2)
	o.addSignature("foo.go", "C", "D", 3)
	o.addSignature("foo.go", "D", "E", 4)
	if msg := o.check(); msg != "" {
		t.Fatalf("expected no warning for chained progression, got: %s", msg)
	}
}

func TestOscillationTrueBackAndForthStillDetected(t *testing.T) {
	// True A->B->A->B oscillation must still fire: the agent re-adds content
	// it previously removed as old_text (direction-aware reversal detection).
	o := newOscillationState()
	o.addSignature("foo.go", "contentA", "contentB", 1)
	o.addSignature("foo.go", "contentB", "contentA", 2)
	o.addSignature("foo.go", "contentA", "contentB", 3)
	if msg := o.check(); msg == "" {
		t.Fatal("expected oscillation warning for true A->B->A->B pattern")
	}
}

func TestOscillationRevertThenRestoreStillDetected(t *testing.T) {
	// A->B then revert to A twice (agent undoes, redoes its own undo) is
	// oscillation even with only two distinct contents.
	o := newOscillationState()
	o.addSignature("foo.go", "A", "B", 1)
	o.addSignature("foo.go", "B", "A", 2)
	o.addSignature("foo.go", "A", "B", 3)
	o.addSignature("foo.go", "B", "A", 4)
	if msg := o.check(); msg == "" {
		t.Fatal("expected oscillation warning for A->B->A->B->A pattern")
	}
}
