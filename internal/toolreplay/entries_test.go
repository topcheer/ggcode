package toolreplay

import (
	"encoding/json"
	"testing"
)

// TestTapeEntriesOrder verifies that Entries returns the recorded entries in
// insertion order, as a copy (the offline grader relies on trajectory order
// and must not disturb replay FIFO state).
func TestTapeEntriesOrder(t *testing.T) {
	tape := NewTape()
	inputs := []string{`{"a":1}`, `{"a":2}`, `{"a":3}`}
	for i, in := range inputs {
		tape.Record(Entry{
			ToolName: "read_file",
			Input:    json.RawMessage(in),
			Result:   Result{Content: string(rune('a' + i))},
		})
	}

	got := tape.Entries()
	if len(got) != len(inputs) {
		t.Fatalf("Entries len = %d, want %d", len(got), len(inputs))
	}
	for i, in := range inputs {
		if string(got[i].Input) != in {
			t.Errorf("Entries[%d].Input = %s, want %s", i, got[i].Input, in)
		}
	}

	// The returned slice must be a copy: mutating it must not affect the tape.
	got[0].ToolName = "mutated"
	if again := tape.Entries(); again[0].ToolName == "mutated" {
		t.Error("Entries returned the internal slice, not a copy")
	}

	// Entries must not consume replay slots: the tape is still fully
	// replayable afterwards.
	e, ok := tape.Lookup("read_file", json.RawMessage(`{"a":1}`), true)
	if !ok || e.Result.Content != "a" {
		t.Errorf("Lookup after Entries: ok=%v entry=%+v", ok, e)
	}
}
