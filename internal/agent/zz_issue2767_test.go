package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// #2767: same-iteration parallel read_file batches are the recommended
// pattern and must NOT trigger the "sequential read_file" hint; only
// cross-iteration serial reads should.

func readCall2767(path string) provider.ToolCallDelta {
	return provider.ToolCallDelta{
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"` + path + `"}`),
	}
}

// Same iteration, 3 parallel read_file calls -- must NOT fire.
func TestIssue2767_ParallelBatchSameIterNoHint(t *testing.T) {
	v := &toolSequenceValidator{history: []seqEntry{}, hintsGiven: map[string]bool{}}
	if g := v.record(readCall2767("/a.go"), 5); g != "" {
		t.Fatalf("first parallel read must not hint: %q", g)
	}
	if g := v.record(readCall2767("/b.go"), 5); g != "" {
		t.Fatalf("second parallel read must not hint: %q", g)
	}
	if g := v.record(readCall2767("/c.go"), 5); g != "" {
		t.Fatalf("third same-iter parallel read must not hint: %q", g)
	}
}

// Cross-iteration serial reads (one per turn) -- MUST fire.
func TestIssue2767_CrossIterationSerialFires(t *testing.T) {
	v := &toolSequenceValidator{history: []seqEntry{}, hintsGiven: map[string]bool{}}
	_ = v.record(readCall2767("/a.go"), 1)
	_ = v.record(readCall2767("/b.go"), 2)
	g := v.record(readCall2767("/c.go"), 3)
	if g == "" {
		t.Fatal("3 cross-iteration serial reads must trigger the hint")
	}
	if !strings.Contains(g, "sequential") {
		t.Fatalf("hint wording lost 'sequential': %q", g)
	}
}

// Mixed: 2 reads in iter 5 + 1 read in iter 6 -- fires (spans 2 iters).
func TestIssue2767_MixedBatchThenNextIterFires(t *testing.T) {
	v := &toolSequenceValidator{history: []seqEntry{}, hintsGiven: map[string]bool{}}
	_ = v.record(readCall2767("/a.go"), 5)
	_ = v.record(readCall2767("/b.go"), 5)
	g := v.record(readCall2767("/c.go"), 6)
	if g == "" {
		t.Fatal("window spanning iters 5+6 must trigger the hint")
	}
}
