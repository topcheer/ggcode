package agent

// #3652 probes: sed skip-marker removal exemption must accept every legal
// sed spelling (any delimiter, single/double/bare quoting), while still
// rejecting actual injections (#1685/#1891 regressions must hold).

import "testing"

func TestIssue3652_DoubleQuotedSlashRemoval(t *testing.T) {
	if !isSedSkipRemoval(`sed -i "s/t.Skip(/t.Log(/" foo_test.go`) {
		t.Fatal("double-quoted s/// removal must be exempt")
	}
}

func TestIssue3652_PipeDelimiterRemoval(t *testing.T) {
	if !isSedSkipRemoval(`sed -i 's|t.Skip(|t.Log(|' foo_test.go`) {
		t.Fatal("pipe-delimiter removal must be exempt")
	}
}

func TestIssue3652_BareExpressionRemoval(t *testing.T) {
	if !isSedSkipRemoval(`sed -i s/t.Skip(/t.Log(/ foo_test.go`) {
		t.Fatal("bare (unquoted) removal must be exempt")
	}
}

func TestIssue3652_CommaDelimiterRemoval(t *testing.T) {
	if !isSedSkipRemoval(`sed -i 's,t.Skip(,t.Log(,' foo_test.go`) {
		t.Fatal("comma-delimiter removal must be exempt")
	}
}

func TestIssue3652_InjectionStillRejected(t *testing.T) {
	// #1685: second ;-separated expression injects the marker.
	if isSedSkipRemoval(`sed -i 's/t.Skip(//g; s/assert/t.Skip(/g' foo_test.go`) {
		t.Fatal("multi-expression injection must NOT be exempt (#1685)")
	}
	// Same injection via pipe delimiter + double quotes.
	if isSedSkipRemoval(`sed -i "s|t.Skip(||g; s|assert|t.Skip(|g" foo_test.go`) {
		t.Fatal("pipe-delimiter injection must NOT be exempt")
	}
	// Plain injection, no removal at all.
	if isSedSkipRemoval(`sed -i 's/assert/t.Skip(/g' foo_test.go`) {
		t.Fatal("pure injection must NOT be exempt")
	}
}

func TestIssue3652_NonSedExpressionWordsIgnored(t *testing.T) {
	// Flags and file names must not be mistaken for expressions.
	if isSedSkipRemoval(`sed -i 's/foo/bar/' internal_test.go`) {
		t.Fatal("no skip marker in pattern: not a removal")
	}
}

func TestIssue3652_SkipMarkerRemovalCommandEndToEnd(t *testing.T) {
	if !isSkipMarkerRemovalCommand(`sed -i "s/t.Skip(/t.Log(/" foo_test.go`) {
		t.Fatal("top-level entry must exempt double-quoted removal")
	}
	if isSkipMarkerRemovalCommand(`sed -i "s/assert/t.Skip(/" foo_test.go`) {
		t.Fatal("top-level entry must reject double-quoted injection")
	}
}
