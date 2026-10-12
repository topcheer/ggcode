package agent

// #3794 companions:
// A: passing pytest output (verify-word + "fail" substring) must NOT be
//    attributed as failure evidence — looksVerify no longer bypasses the
//    failure-sign gate, and the `path::` zero-digit regex branch is gone
//    (`tests/x.py::test_foo PASSED` extracts no error file).
// B: recency alone (disjoint edit, no file/dir/package overlap with any
//    error reference) must score ZERO — the recency bonus requires an
//    actual evidence hit at some tier.

import (
	"strings"
	"testing"
)

func TestIssue3794_A_PassingPytestNotAttributed(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "pkg/tests/test_x.py", 2)

	// Passing pytest run: verify word present, "failed" substring present
	// (summary echo), but zero actual failures.
	out := `$ pytest tests/ --lf
tests/test_x.py::test_failure_retry PASSED
================= 0 failed, 12 passed in 3.4s =================`
	if hint := s.attributeFailure(out); hint != "" {
		t.Fatalf("passing pytest output must not be attributed, got: %s", hint)
	}
}

func TestIssue3794_A_DoubleColonNoDigitsExtractsNothing(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "pkg/tests/test_x.py", 2)
	// `file.py::name` (zero digits) must not register as an error file;
	// combined with A's gate this output yields no suspect.
	out := `$ pytest tests/ --lf
tests/test_x.py::test_failure_retry PASSED
check failed: see tests/test_x.py::test_failure_retry`
	if hint := s.attributeFailure(out); hint == "" {
		// If something fires, it must NOT cite test_x.py as error-referenced
		// (the :: form carried no line evidence).
		return
	} else if strings.Contains(hint, "references this file") {
		t.Fatalf("zero-digit :: extraction cited as file evidence: %s", hint)
	}
}

func TestIssue3794_B_DisjointRecencyScoresZero(t *testing.T) {
	s := newCausalAttributionState()
	// Package A build failure; the only recent edit is in disjoint package B.
	s.recordEdit("edit_file", "internal/bbb/unrelated.go", 5)

	out := `# internal/aaa
./internal/aaa/broken.go:12:5: cannot use x (variable of type int) as string value
FAIL	github.com/ggcode/internal/aaa [build failed]`

	if hint := s.attributeFailure(out); hint != "" {
		t.Fatalf("recency-only disjoint edit must not clear the suspect threshold, got: %s", hint)
	}
	// Zero-evidence path must not pollute the persisted suspect either.
	if step, crs := s.takeFinalSuspect(); step != nil && crs != 0 {
		t.Fatalf("disjoint edit persisted as suspect: %+v crs=%d", step, crs)
	}
}

func TestIssue3794_B_RealOverlapStillAttributed(t *testing.T) {
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "internal/agent/causal_attribution.go", 4)

	out := `# internal/agent
./causal_attribution.go:42:10: undefined: fooBar
FAIL	github.com/ggcode/internal/agent [build failed]`

	hint := s.attributeFailure(out)
	if hint == "" {
		t.Fatal("real overlap must still attribute")
	}
	if !strings.Contains(hint, "references this file") {
		t.Fatalf("file-tier match must keep the file-evidence wording, got: %s", hint)
	}
}
