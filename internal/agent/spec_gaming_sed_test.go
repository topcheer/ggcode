package agent

import "testing"

// #1891: the #1685 multi-expression fix was ineffective - chunks after the
// first "; " still carried the `s` command prefix, so pattern/replacement
// extraction was offset and the injected skip marker landed at index 2 where
// containsAnySkipMarker never looked. The exact commit-message case must NOT
// be exempted.
func TestSedMultiExpressionInjectionNotExempt(t *testing.T) {
	// The precise case from the 0e6a1d2a commit message: a legitimate
	// t.Skip( removal followed by a second expression that INJECTS it.
	cmd := `sed -i 's/t.Skip(//g; s/assert/t.Skip(/g' x_test.go`
	if isSedSkipRemoval(cmd) {
		t.Fatal("multi-expression injection must not be exempted (marker smuggled via the second s command)")
	}

	// Whitespace variants of the same smuggling.
	cmd = `sed -i 's/t.Skip(//g;s/assert/t.Skip(/g' x_test.go`
	if isSedSkipRemoval(cmd) {
		t.Fatal("no-space variant must not be exempted")
	}

	// A legitimate single-expression removal stays exempt.
	cmd = `sed -i 's/t.Skip(//g' x_test.go`
	if !isSedSkipRemoval(cmd) {
		t.Fatal("legitimate single removal must stay exempt")
	}

	// Multiple legitimate removals stay exempt.
	cmd = `sed -i 's/t.Skip(//g; s/Skip(/Skip(/g' x_test.go`
	if !isSedSkipRemoval(cmd) {
		t.Fatal("multiple legitimate removals must stay exempt")
	}
}
