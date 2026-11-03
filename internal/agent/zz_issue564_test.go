package agent

import (
	"strings"
	"testing"
)

func TestIssue564_ErrorCounterComparisonNotFlagged(t *testing.T) {
	src := `package x
func f(errorCount, errorTotal int) bool {
	if errorCount == errorTotal {
		return true
	}
	return false
}
`
	got := checkSuspiciousComparison("counter.go", "", src)
	if got != "" {
		t.Errorf("errorCount == errorTotal should not be flagged, got: %s", got)
	}
}

func TestIssue564_RealErrorComparisonStillFlagged(t *testing.T) {
	src := `package x
func f(errRead, errWrite error) bool {
	if errRead == errWrite {
		return true
	}
	return false
}
`
	got := checkSuspiciousComparison("errs.go", "", src)
	if !strings.Contains(got, "errors.Is") {
		t.Errorf("errRead == errWrite should still be flagged, got: %q", got)
	}
}

func TestIssue564_SelectorCounterFieldNotFlagged(t *testing.T) {
	src := `package x
type s struct{ errorCount, errorTotal int }
func f(v s) bool {
	if v.errorCount == v.errorTotal {
		return true
	}
	return false
}
`
	got := checkSuspiciousComparison("sel.go", "", src)
	if got != "" {
		t.Errorf("v.errorCount == v.errorTotal should not be flagged, got: %s", got)
	}
}

func TestIssue564_FloatZeroEqualityNotFlagged(t *testing.T) {
	src := `package x
func f(v float64) bool {
	if v == 0.0 {
		return true
	}
	return false
}
`
	got := checkSuspiciousComparison("zero.go", "", src)
	if got != "" {
		t.Errorf("v == 0.0 should not be flagged, got: %s", got)
	}
}

func TestIssue564_FloatNonZeroEqualityStillFlagged(t *testing.T) {
	src := `package x
func f(v float64) bool {
	if v == 0.1 {
		return true
	}
	return false
}
`
	got := checkSuspiciousComparison("nz.go", "", src)
	if !strings.Contains(got, "float equality") {
		t.Errorf("v == 0.1 should still be flagged, got: %q", got)
	}
}

func TestIssue564_TestFileSecretExemption(t *testing.T) {

	src := `package x
const awsDocsKey = "AKIAIOSFODNN7EXAMPLE"
const stripeTestKey = "sk_test_4eC39HqLyjWDarjtT1zdc"
`
	if got := checkHardcodedSecrets("fixture_test.go", "", src); len(got) != 0 {
		t.Errorf("_test.go example keys should be exempt, got: %v", got)
	}
}

func TestIssue564_NonTestFileSecretStillFlagged(t *testing.T) {
	src := `package x
const awsDocsKey = "AKIAIOSFODNN7EXAMPLE"
`
	if got := checkHardcodedSecrets("config.go", "", src); len(got) == 0 {
		t.Errorf("non-test file with example-shaped key should still be flagged")
	}
}
