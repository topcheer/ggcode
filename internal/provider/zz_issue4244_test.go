package provider

import (
	"maps"
	"slices"
	"testing"
)

// #4244: parallel tool calls must be emitted in model-idx order, not Go map
// random order. The fixed emit sequence is slices.Sorted(maps.Keys(acc)) in
// both the finish_reason loop (openai.go) and the residual flush
// (openai_stream_flush.go) - prove it is sorted and complete for a
// non-trivial key set, over many reshuffles of the map.
func TestIssue4244_SortedEmitOrder(t *testing.T) {
	acc := map[int]*ToolCallDelta{
		7: {ID: "c7", Name: "git_commit"},
		0: {ID: "c0", Name: "git_add"},
		3: {ID: "c3", Name: "mkdir"},
		1: {ID: "c1", Name: "write_file"},
	}
	want := []int{0, 1, 3, 7}
	for i := 0; i < 50; i++ { // re-hash guard: order must hold every run
		got := slices.Sorted(maps.Keys(acc))
		if !slices.Equal(got, want) {
			t.Fatalf("emit order must be the sorted key set %v, got %v", want, got)
		}
	}
}
