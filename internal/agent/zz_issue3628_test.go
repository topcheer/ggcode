package agent

// #3628 probe: isSedSkipRemoval must recognize the double-quoted sed form.
// The single-quote split ('s/) never matched `sed -i "s/t.Skip(//g"` so a
// legitimate skip-REMOVAL failed the exemption and was reported as
// tampering.

import "testing"

func TestIssue3628_DoubleQuotedSedExempt(t *testing.T) {
	if !isSedSkipRemoval(`sed -i "s/t.Skip(//g" *_test.go`) {
		t.Fatal("double-quoted skip removal not exempted")
	}
	// Single-quote form unchanged (#1685/#1891 protections intact).
	if !isSedSkipRemoval(`sed -i 's/t.Skip(//g' *_test.go`) {
		t.Fatal("single-quoted skip removal regressed")
	}
	// Injection in the replacement is still tampering, both quote styles.
	if isSedSkipRemoval(`sed -i "s/assert/t.Skip(/g" *_test.go`) {
		t.Fatal("double-quoted skip injection wrongly exempted")
	}
	if isSedSkipRemoval(`sed -i 's/assert/t.Skip(/g' *_test.go`) {
		t.Fatal("single-quoted skip injection wrongly exempted")
	}
}
