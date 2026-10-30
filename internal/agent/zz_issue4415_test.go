//go:build goolm

package agent

// #4415 V1 regression tests: the #4403 filters over-matched - "--- FAIL:"
// per-test lines were eaten by the flag-echo filter and "10 failed"
// summaries by the bare "0 failed" substring.

import (
	"strings"
	"testing"
)

func TestIssue4415_PerTestFAILLinesKept(t *testing.T) {
	out := strings.Join([]string{
		"=== RUN   TestFoo",
		"--- FAIL: TestFoo (0.01s)",
		"    foo_test.go:12: assertion mismatch",
		"FAIL github.com/x/y 0.5s",
		"--- PASS: TestBar (0.00s)",
	}, "\n")
	got := extractErrorLines(out)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "--- FAIL: TestFoo") {
		t.Fatalf("go test per-test '--- FAIL:' lines must be kept, got %q (#4415)", joined)
	}
	if !strings.Contains(joined, "FAIL github.com/x/y") {
		t.Fatalf("package FAIL summary must be kept, got %q (#4415)", joined)
	}
	// Flag echoes are still filtered.
	if strings.Contains(joined, "--fail-fast") {
		t.Fatalf("flag echoes must stay filtered (#4415)")
	}
	got2 := extractErrorLines("--fail-fast -errorflags=none")
	if len(got2) != 0 {
		t.Fatalf("flag echo line must be filtered entirely, got %v (#4415)", got2)
	}
}

func TestIssue4415_TenFailedSummaryKept(t *testing.T) {
	out := "10 failed, 2 passed in 3.41s"
	got := extractErrorLines(out)
	if len(got) == 0 {
		t.Fatalf("'10 failed' real failure summary must not be swallowed by the zero-count filter (#4415)")
	}
	// True zero summaries are still filtered.
	got2 := extractErrorLines("0 failed, 3 passed, 1 skipped")
	for _, l := range got2 {
		if strings.Contains(l, "0 failed") {
			t.Fatalf("'0 failed' passing summary must stay filtered (#4415)")
		}
	}
}
