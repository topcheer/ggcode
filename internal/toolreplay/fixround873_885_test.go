package toolreplay

// Guard tests for the #873-#885 fix round (tape portion).

import (
	"encoding/json"
	"testing"
)

// TestLookupNonStrictLastWins (#877): non-strict mode must serve the LAST
// recorded result for a key, not the first (comment promises last-wins).
func TestLookupNonStrictLastWins(t *testing.T) {
	tape := NewTape()
	rec := func(v string) {
		tape.Record(Entry{ToolName: "read_file",
			Input:  json.RawMessage(`{"path":"/x"}`),
			Result: Result{Content: v}})
	}
	rec("first-result")
	rec("second-result")

	e1, ok := tape.Lookup("read_file", json.RawMessage(`{"path":"/x"}`), false)
	if !ok || e1.Result.Content != "second-result" {
		t.Fatalf("non-strict lookup returned %q ok=%v, want second-result", e1.Result.Content, ok)
	}
	// Repeated lookups must stay on the last entry.
	e2, _ := tape.Lookup("read_file", json.RawMessage(`{"path":"/x"}`), false)
	if e2.Result.Content != "second-result" {
		t.Fatalf("repeat lookup drifted to %q", e2.Result.Content)
	}
}

// TestLookupStrictFIFOStillWorks: regression check - strict mode still walks
// the recorded sequence in order.
func TestLookupStrictFIFOStillWorks(t *testing.T) {
	tape := NewTape()
	tape.Record(Entry{ToolName: "t", Input: json.RawMessage(`{}`), Result: Result{Content: "a"}})
	tape.Record(Entry{ToolName: "t", Input: json.RawMessage(`{}`), Result: Result{Content: "b"}})
	e1, _ := tape.Lookup("t", json.RawMessage(`{}`), true)
	e2, _ := tape.Lookup("t", json.RawMessage(`{}`), true)
	if e1.Result.Content != "a" || e2.Result.Content != "b" {
		t.Fatalf("strict FIFO broken: %q then %q", e1.Result.Content, e2.Result.Content)
	}
	if _, ok := tape.Lookup("t", json.RawMessage(`{}`), true); ok {
		t.Fatal("third strict lookup should be exhausted")
	}
}
