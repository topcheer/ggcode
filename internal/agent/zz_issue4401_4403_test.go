//go:build goolm

package agent

// #4401/#4402/#4403 regression tests (business-logic audit round r5).

import (
	"strings"
	"testing"
)

// #4402: compound commands lose the pytest exit-5 amnesty, mirroring the
// 127 branch (#1522 case B). `go test ./pkg; pytest` with a compile
// failure exits 5 from the pytest tail (0 tests collected) - the old code
// returned Passed=true and swallowed the real failure ("silent green
// chain").
func TestIssue4402_Exit5CompoundGuard(t *testing.T) {
	cases := []struct {
		cmd  string
		code int
		want bool
	}{
		{"pytest tests/", 5, true},                 // simple: amnesty holds
		{"go test ./pkg; pytest tests/", 5, false}, // compound ; - real failure swallowed before
		{"go vet ./... || pytest", 5, false},       // compound || - fallback side
		{"go test ./... && pytest", 5, false},      // compound &&
		{"pytest tests/", 1, false},                // genuine pytest failure
		{"make build", 5, false},                   // exit 5 without pytest
	}
	for _, c := range cases {
		if got := isNonFailureExit(c.cmd, c.code); got != c.want {
			t.Fatalf("isNonFailureExit(%q, %d) = %v, want %v (#4402)", c.cmd, c.code, got, c.want)
		}
	}
}

// #4403: success negations ("0 failed", "0 failures", "no error",
// "without error") and flag echoes ("--fail-fast") must not be extracted
// as error lines for ratchet learning; genuine error lines still are.
func TestIssue4403_ExtractErrorLinesExcludesSuccessNegations(t *testing.T) {
	out := strings.Join([]string{
		"--- PASS: TestFoo (0.01s)",
		"0 failed, 3 passed, 1 skipped",
		"package ./pkg2 exited without error",
		"--fail-fast -errorflags=none",
		"FAIL github.com/topcheer/ggcode/internal/agent [build failed]",
		"# github.com/x/y",
		"undefined: foo.Bar",
	}, "\n")
	got := extractErrorLines(out)
	joined := strings.Join(got, "\n")
	for _, bogus := range []string{"0 failed", "without error", "--fail-fast"} {
		if strings.Contains(joined, bogus) {
			t.Fatalf("success negation/flag echo must not be extracted, got %q (#4403)", joined)
		}
	}
	if !strings.Contains(joined, "FAIL github.com") {
		t.Fatalf("genuine FAIL line must be extracted, got %q (#4403)", joined)
	}
	if !strings.Contains(joined, "undefined: foo.Bar") {
		t.Fatalf("genuine undefined line must be extracted, got %q (#4403)", joined)
	}
}

// #4401: "make sure / making sure" is verification-intent framing - the
// caveat table must veto the bare-"done" declaration in "Running tests to
// make sure everything is done." just like "check if" does.
func TestIssue4401_MakeSureCaveat(t *testing.T) {
	check := func(text string) bool {
		lower := strings.ToLower(text)
		decl := strings.Index(lower, "done")
		if decl < 0 {
			t.Fatalf("test text must contain the declaration: %q", text)
		}
		return sdHasCaveatWindowed(lower, decl, decl+len("done"))
	}
	if !check("running tests to make sure everything is done. ") {
		t.Fatalf("'make sure' must veto the done declaration (#4401)")
	}
	if !check("making sure the build is green before done ") {
		t.Fatalf("'making sure' must veto the done declaration (#4401)")
	}
	// Control: a plain completion claim has no caveat.
	if check("all tests pass, everything is done. ") {
		t.Fatalf("plain completion claim must not trip a caveat (#4401)")
	}
}
