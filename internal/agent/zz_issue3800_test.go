package agent

import (
	"testing"
)

func TestIssue3800_BareSubtreeWildcardScope(t *testing.T) {
	got := coverageExtractVerifyScopes("go test internal/agent/...")
	if len(got) != 1 || got[0] != "internal/agent" {
		t.Fatalf("bare subtree wildcard must normalize to prefix scope, got %v", got)
	}

	got = coverageExtractVerifyScopes("go test ./internal/agent/...")
	if len(got) != 1 || got[0] != "internal/agent" {
		t.Fatalf("./-anchored subtree regression, got %v", got)
	}

	got = coverageExtractVerifyScopes("go test internal/agent/... internal/config")
	if len(got) != 2 || got[0] != "internal/agent" || got[1] != "internal/config" {
		t.Fatalf("mixed bare scopes mis-extracted, got %v", got)
	}
}

func TestIssue3800_BareSubtreeCoversPackages(t *testing.T) {

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
