package agent

import (
	"strings"
	"testing"
)

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

	w := checkWriteIntegrity("main.go", rcWiringFixture, rcWiringFixture)
	if strings.Contains(w, "Too many return statements") {
		t.Fatalf("pre-existing 7-return function must be delta-suppressed, got: %q", w)
	}
}

func TestFuzzTargetsExemptFromReturnCount(t *testing.T) {

	if w := checkExcessiveReturns("seed_test.go", "", rcFuzzFixture); w != nil {
		t.Fatalf("Fuzz-prefixed function in _test.go must be exempt from return-count (#2558), got: %v", w)
	}

	if w := checkExcessiveReturns("parser.go", "", rcFuzzFixture); len(w) == 0 {
		t.Fatal("Fuzz-named function in production code must stay checked (test-file-only exemption, #1193)")
	}
}

func TestIsTestOrBenchFunctionCoversFuzzPrefix(t *testing.T) {

	for _, name := range []string{"FuzzParser", "Fuzz"} {
		if !isTestOrBenchFunction(name) {
			t.Errorf("isTestOrBenchFunction(%q) must be true (#2558)", name)
		}
	}

	for _, name := range []string{"fuzzParser", "MyFuzz", "Confuse"} {
		if isTestOrBenchFunction(name) {
			t.Errorf("isTestOrBenchFunction(%q) must be false", name)
		}
	}
}

func TestFuzzTargetsExemptFromParamCount(t *testing.T) {

	src := `package main

import "testing"

func FuzzRoundTrip(f *testing.F, a, b, c, d, e int) {
	_ = a + b + c + d + e
}
`
	if w := checkExcessiveParams("roundtrip_test.go", "", src); w != nil {
		t.Fatalf("Fuzz-prefixed function in _test.go must be exempt from param-count (#2558), got: %v", w)
	}

	ctrl := strings.Replace(src, "FuzzRoundTrip", "roundTripCheck", 1)
	if w := checkExcessiveParams("roundtrip_test.go", "", ctrl); len(w) == 0 {
		t.Fatal("6-param non-exempt function in _test.go must still be flagged by param-count")
	}
}
