package agent

// #2558: the return-count detector (checkExcessiveReturns, SonarQube S114,
// 6+ return statements) was fully implemented and unit tested through
// #142/#157/#1193 but NEVER registered in the write integrity registry - a
// zero-wiring dead detector (same class as #499 "append-ignored"). Unit
// tests of the check function cannot protect against unwiring regressions,
// so these tests pin the REGISTRY ENTRY plus end-to-end firing through
// checkWriteIntegrity, and the Fuzz-name exemption gap the issue flagged in
// the shared isTestOrBenchFunction helper (param_count_check.go), which both
// return-count and param-count consult.

import (
	"strings"
	"testing"
)

// rcWiringFixture is a production file whose classify function has 7 return
// statements (threshold 6, SonarQube S114 default).
const rcWiringFixture = `package main

func classify(n int) string {
	if n < 0 {
		return "neg"
	}
	if n == 0 {
		return "zero"
	}
	if n < 10 {
		return "small"
	}
	if n < 100 {
		return "medium"
	}
	if n < 1000 {
		return "large"
	}
	if n < 10000 {
		return "xl"
	}
	return "huge"
}
`

// rcFuzzFixture is the seed-corpus early-exit guard shape the issue called
// out: a fuzz target whose OWN body (not the f.Fuzz closure - countReturns
// does not descend into FuncLit) carries 6+ returns.
const rcFuzzFixture = `package main

import "testing"

func FuzzParser(f *testing.F, data string) bool {
	if data == "" {
		return false
	}
	if data[0] == 'x' {
		return false
	}
	if len(data) > 10 {
		return false
	}
	if len(data) > 100 {
		return false
	}
	if len(data) > 1000 {
		return false
	}
	return true
}
`

func TestReturnCountRegisteredInWriteIntegrityRegistry(t *testing.T) {
	registerAllChecks()
	found := false
	for _, chk := range allChecks {
		if chk.Name == "return-count" {
			found = true
			goLang := false
			for _, l := range chk.Langs {
				if l == LangGo {
					goLang = true
				}
			}
			if !goLang {
				t.Error("return-count entry must be restricted to LangGo")
			}
			break
		}
	}
	if !found {
		t.Fatal("return-count detector is not registered in the write integrity registry (#2558 zero-wiring defect)")
	}
}

func TestReturnCountFiresThroughCheckWriteIntegrity(t *testing.T) {
	registerAllChecks()
	w := checkWriteIntegrity("main.go", "", rcWiringFixture)
	if !strings.Contains(w, "Too many return statements") {
		t.Fatalf("wired return-count check must flag a NEW 7-return function via checkWriteIntegrity (#2558), got: %q", w)
	}
}

func TestReturnCountPreExistingInstanceDeltaSuppressed(t *testing.T) {
	registerAllChecks()
	// A write that leaves the pre-existing 7-return function untouched must
	// not re-report it: the detector is delta-aware by fingerprint multiset.
	w := checkWriteIntegrity("main.go", rcWiringFixture, rcWiringFixture)
	if strings.Contains(w, "Too many return statements") {
		t.Fatalf("pre-existing 7-return function must be delta-suppressed, got: %q", w)
	}
}

func TestFuzzTargetsExemptFromReturnCount(t *testing.T) {
	// In _test.go the Fuzz prefix is exempt - same rationale as Test/Benchmark:
	// seed-corpus early-exit guards legitimately use many returns (#2558).
	if w := checkExcessiveReturns("seed_test.go", "", rcFuzzFixture); w != nil {
		t.Fatalf("Fuzz-prefixed function in _test.go must be exempt from return-count (#2558), got: %v", w)
	}
	// The same name in a PRODUCTION file stays checked: the exemption is
	// scoped to _test.go (#1193), so a business function named FuzzParser
	// is not silently hidden by the new prefix.
	if w := checkExcessiveReturns("parser.go", "", rcFuzzFixture); len(w) == 0 {
		t.Fatal("Fuzz-named function in production code must stay checked (test-file-only exemption, #1193)")
	}
}

func TestIsTestOrBenchFunctionCoversFuzzPrefix(t *testing.T) {
	// Prefix semantics mirror go test's own entry-point rule: any name
	// starting with Fuzz is a fuzz target (as with Test/Benchmark).
	for _, name := range []string{"FuzzParser", "Fuzz"} {
		if !isTestOrBenchFunction(name) {
			t.Errorf("isTestOrBenchFunction(%q) must be true (#2558)", name)
		}
	}
	// Lowercase or embedded occurrences must NOT match.
	for _, name := range []string{"fuzzParser", "MyFuzz", "Confuse"} {
		if isTestOrBenchFunction(name) {
			t.Errorf("isTestOrBenchFunction(%q) must be false", name)
		}
	}
}

func TestFuzzTargetsExemptFromParamCount(t *testing.T) {
	// param_count_check.go consults the same helper (#1187/#2558): a fuzz
	// target with 6 named params (f *testing.F + 5 corpus params, threshold 6
	// including receiver) in _test.go must be exempt.
	src := `package main

import "testing"

func FuzzRoundTrip(f *testing.F, a, b, c, d, e int) {
	_ = a + b + c + d + e
}
`
	if w := checkExcessiveParams("roundtrip_test.go", "", src); w != nil {
		t.Fatalf("Fuzz-prefixed function in _test.go must be exempt from param-count (#2558), got: %v", w)
	}
	// Control: non-exempt name, same arity, same file - the exemption is
	// name-scoped, not file-wide.
	ctrl := strings.Replace(src, "FuzzRoundTrip", "roundTripCheck", 1)
	if w := checkExcessiveParams("roundtrip_test.go", "", ctrl); len(w) == 0 {
		t.Fatal("6-param non-exempt function in _test.go must still be flagged by param-count")
	}
}
