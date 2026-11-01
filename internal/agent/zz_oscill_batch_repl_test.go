package agent

import (
	"encoding/json"
	"testing"
)

// Detector self-health scan round 3 found recordEdit handled only 2 of 9
// source-mutating tools: a batch_replace A->B followed by B->A (classic
// semantic oscillation) was silently unrecorded. multi_file_edit's
// per-file edits were equally invisible. Threshold is 2 content reversals,
// so each test drives a full double round-trip.
func TestOscillationBatchReplaceReversal(t *testing.T) {
	o := newOscillationState()
	o.recordEdit("batch_replace", json.RawMessage(`{"pattern":"alpha","replacement":"beta","files":["a.go","b.go"]}`), 1)
	o.recordEdit("batch_replace", json.RawMessage(`{"pattern":"beta","replacement":"alpha","files":["a.go","b.go"]}`), 2)
	o.recordEdit("batch_replace", json.RawMessage(`{"pattern":"alpha","replacement":"beta","files":["a.go","b.go"]}`), 3)
	o.recordEdit("batch_replace", json.RawMessage(`{"pattern":"beta","replacement":"alpha","files":["a.go","b.go"]}`), 4)
	if w := o.check(); w == "" {
		t.Fatal("batch_replace double round-trip must be detected as oscillation")
	}
}

func TestOscillationMultiFileEditReversal(t *testing.T) {
	o := newOscillationState()
	fwd := `{"files":[{"path":"x.go","edits":[{"old_text":"foo","new_text":"bar"}]}]}`
	rev := `{"files":[{"path":"x.go","edits":[{"old_text":"bar","new_text":"foo"}]}]}`
	o.recordEdit("multi_file_edit", json.RawMessage(fwd), 1)
	o.recordEdit("multi_file_edit", json.RawMessage(rev), 2)
	o.recordEdit("multi_file_edit", json.RawMessage(fwd), 3)
	o.recordEdit("multi_file_edit", json.RawMessage(rev), 4)
	if w := o.check(); w == "" {
		t.Fatal("multi_file_edit per-file double round-trip must be detected as oscillation")
	}
}
