package agent

import "testing"

func TestIssue3628_DoubleQuotedSedExempt(t *testing.T) {
	if !isSedSkipRemoval(`sed -i "s/t.Skip(//g" *_test.go`) {
		t.Fatal("double-quoted skip removal not exempted")
	}

	if !isSedSkipRemoval(`sed -i 's/t.Skip(//g' *_test.go`) {
		t.Fatal("single-quoted skip removal regressed")
	}

	if isSedSkipRemoval(`sed -i "s/assert/t.Skip(/g" *_test.go`) {
		t.Fatal("double-quoted skip injection wrongly exempted")
	}
	if isSedSkipRemoval(`sed -i 's/assert/t.Skip(/g' *_test.go`) {
		t.Fatal("single-quoted skip injection wrongly exempted")
	}
}
