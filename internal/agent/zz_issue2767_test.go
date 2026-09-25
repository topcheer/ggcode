package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestIssue2767_ParallelBatchNotSequential pins the #2767 fix: a single
// iteration's parallel batch of 3 read_file calls (executed serially, each
// recorded with the same iteration number) must NOT fire the
// "sequential reads" hint - the calls are parallel, not sequential.
func TestIssue2767_ParallelBatchNotSequential(t *testing.T) {
	v := newToolSequenceValidator()
	const iter = 5
	for _, path := range []string{"/a.go", "/b.go", "/c.go"} {
		tc := provider.ToolCallDelta{
			Name:      "read_file",
			Arguments: []byte(`{"path":` + quotePath2767(t, path) + `}`),
		}
		if got := v.record(tc, iter); got != "" {
			t.Fatalf("same-iteration parallel batch read of %s fired guidance: %q", path, got)
		}
	}
}

// TestIssue2767_CrossIterationSequentialStillFires pins that the true
// anti-pattern - one read_file per iteration across 3 distinct iterations -
// still triggers the hint after the fix.
func TestIssue2767_CrossIterationSequentialStillFires(t *testing.T) {
	v := newToolSequenceValidator()
	for i, path := range []string{"/a.go", "/b.go", "/c.go"} {
		tc := provider.ToolCallDelta{
			Name:      "read_file",
			Arguments: []byte(`{"path":` + quotePath2767(t, path) + `}`),
		}
		got := v.record(tc, i+1)
		if i+1 < 3 {
			if got != "" {
				t.Fatalf("iter %d fired guidance prematurely: %q", i+1, got)
			}
			continue
		}
		if !strings.Contains(got, "sequential read_file") || !strings.Contains(got, "multi_file_read") {
			t.Fatalf("3rd cross-iteration read should fire the sequential-reads hint, got %q", got)
		}
	}
}

// TestIssue2767_BatchThenNextIterationCountsOnce ensures a parallel batch of
// 2 followed by a single read in the NEXT iteration (1 cross-iteration
// predecessor + current) stays below the threshold.
func TestIssue2767_BatchThenNextIterationCountsOnce(t *testing.T) {
	v := newToolSequenceValidator()
	for _, path := range []string{"/a.go", "/b.go"} {
		tc := provider.ToolCallDelta{
			Name:      "read_file",
			Arguments: []byte(`{"path":` + quotePath2767(t, path) + `}`),
		}
		if got := v.record(tc, 1); got != "" {
			t.Fatalf("batch read fired guidance: %q", got)
		}
	}
	tc := provider.ToolCallDelta{Name: "read_file", Arguments: []byte(`{"path":"/c.go"}`)}
	if got := v.record(tc, 2); got != "" {
		t.Fatalf("batch(2)+next-iteration read has only 1 sequential predecessor; must not fire, got %q", got)
	}
}

func quotePath2767(t *testing.T, s string) string {
	t.Helper()
	return `"` + s + `"`
}
