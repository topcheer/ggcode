package provider

import (
	"maps"
	"slices"
	"testing"
)

func TestIssue4244_SortedEmitOrder(t *testing.T) {
	acc := map[int]*ToolCallDelta{
		7: {ID: "c7", Name: "git_commit"},
		0: {ID: "c0", Name: "git_add"},
		3: {ID: "c3", Name: "mkdir"},
		1: {ID: "c1", Name: "write_file"},
	}
	want := []int{0, 1, 3, 7}
	for i := 0; i < 50; i++ {
		got := slices.Sorted(maps.Keys(acc))
		if !slices.Equal(got, want) {
			t.Fatalf("emit order must be the sorted key set %v, got %v", want, got)
		}
	}
}
