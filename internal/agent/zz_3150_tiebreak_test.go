package agent

// #3150 V2 regression: on a CRS tie the top suspect must resolve to the
// MORE RECENT edit (design comment: "Higher for more recent edits -
// recency bias in causality"). Strict `>` kept the oldest-iteration
// edit because results are chronological (oldest first) and ties never
// displaced results[0].
//
// Tie construction under the live weights (match=50, recency=10/step,
// sameDir=5, samePkg=8, threshold=25):
//   edit A (rank 1, error-file match):   50 + 10  = 60
//   edit F (rank 6, no match, no dir/pkg): 0 + 60 = 60
// The four middle edits are unrelated files so they score below both.

import (
	"strings"
	"testing"
)

func TestCausalAttribution_TieBreakPrefersRecentEdit(t *testing.T) {
	s := newCausalAttributionState()

	// Oldest edit: matches the error file exactly.
	s.recordEdit("edit_file", "internal/agent/zz_tie_err.go", 1)
	// Four unrelated middle edits (different dirs, no match).
	s.recordEdit("edit_file", "internal/im/zz_tie_m1.go", 2)
	s.recordEdit("edit_file", "internal/im/zz_tie_m2.go", 3)
	s.recordEdit("edit_file", "internal/im/zz_tie_m3.go", 4)
	s.recordEdit("edit_file", "internal/im/zz_tie_m4.go", 5)
	// Newest edit: unrelated file, no match, no dir/pkg bonus.
	s.recordEdit("edit_file", "internal/tool/zz_tie_latest.go", 6)

	output := `# internal/agent
./zz_tie_err.go:42:10: undefined: fooBar
FAIL	github.com/topcheer/ggcode/internal/agent [build failed]`

	hint := s.attributeFailure(output)
	if hint == "" {
		t.Fatal("expected attribution guidance, got empty")
	}
	if !strings.Contains(hint, "zz_tie_latest.go") {
		t.Errorf("tie must resolve to the MORE RECENT edit (zz_tie_latest.go), got: %s", hint)
	}
	if strings.Contains(hint, "zz_tie_err.go") {
		t.Errorf("tie must not resolve to the OLDER edit (zz_tie_err.go), got: %s", hint)
	}
}
