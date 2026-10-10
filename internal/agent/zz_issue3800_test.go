package agent

// #3800 companion: coverageExtractVerifyScopes must strip the trailing
// `...` wildcard from BARE relative subtrees (`go test internal/agent/...`)
// the same way the ./-anchored branch does - otherwise the scope stays the
// literal `internal/agent/...`, coveragePkgInScope's equality/prefix
// matching fails, and explicitly verified packages get UNVERIFIED false
// positives.

import (
	"testing"
)

func TestIssue3800_BareSubtreeWildcardScope(t *testing.T) {
	got := coverageExtractVerifyScopes("go test internal/agent/...")
	if len(got) != 1 || got[0] != "internal/agent" {
		t.Fatalf("bare subtree wildcard must normalize to prefix scope, got %v", got)
	}
	// ./-anchored form must keep behaving identically (#3749 semantics).
	got = coverageExtractVerifyScopes("go test ./internal/agent/...")
	if len(got) != 1 || got[0] != "internal/agent" {
		t.Fatalf("./-anchored subtree regression, got %v", got)
	}
	// Multi-package bare list with one wildcard mixes prefix + plain scopes.
	got = coverageExtractVerifyScopes("go test internal/agent/... internal/config")
	if len(got) != 2 || got[0] != "internal/agent" || got[1] != "internal/config" {
		t.Fatalf("mixed bare scopes mis-extracted, got %v", got)
	}
}

func TestIssue3800_BareSubtreeCoversPackages(t *testing.T) {
	// End-to-end through coveragePkgInScope: the bare subtree command's
	// scope must cover both the package itself and its subpackages while
	// excluding outside trees (pre-fix every match failed -> UNVERIFIED
	// false positives for explicitly verified packages).
	scopes := coverageExtractVerifyScopes("go test internal/agent/...")
	if len(scopes) != 1 {
		t.Fatalf("setup: expected one scope, got %v", scopes)
	}
	for _, tc := range []struct {
		pkg  string
		want bool
	}{
		{"internal/agent", true},
		{"internal/agent/sub", true},
		{"internal/other", false},
	} {
		if got := coveragePkgInScope(tc.pkg, scopes[0]); got != tc.want {
			t.Fatalf("coveragePkgInScope(%q, %q) = %v, want %v", tc.pkg, scopes[0], got, tc.want)
		}
	}
}
