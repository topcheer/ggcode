package agent

import "testing"

func TestSedMultiExpressionInjectionNotExempt(t *testing.T) {

	cmd := `sed -i 's/t.Skip(//g; s/assert/t.Skip(/g' x_test.go`
	if isSedSkipRemoval(cmd) {
		t.Fatal("multi-expression injection must not be exempted (marker smuggled via the second s command)")
	}

	cmd = `sed -i 's/t.Skip(//g;s/assert/t.Skip(/g' x_test.go`
	if isSedSkipRemoval(cmd) {
		t.Fatal("no-space variant must not be exempted")
	}

	cmd = `sed -i 's/t.Skip(//g' x_test.go`
	if !isSedSkipRemoval(cmd) {
		t.Fatal("legitimate single removal must stay exempt")
	}

	cmd = `sed -i 's/t.Skip(//g; s/Skip(/Skip(/g' x_test.go`
	if !isSedSkipRemoval(cmd) {
		t.Fatal("multiple legitimate removals must stay exempt")
	}
}
