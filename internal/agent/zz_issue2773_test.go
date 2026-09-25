package agent

import (
	"reflect"
	"testing"
)

// TestIssue2773GoFileListArgsMapToPackageScopes pins #2773: `go test` accepts
// file-list args (./pkg/a.go ./pkg/a_test.go). Both scope extractors
// collected the .go path itself as a "package scope", so the explicitly
// verified package never matched coveragePkgInScope and got flagged
// UNVERIFIED (verify_coverage_gap) or never repaid its debt
// (verification_debt). File args must map to their package directory.
func TestIssue2773GoFileListArgsMapToPackageScopes(t *testing.T) {
	cmd := "go test ./internal/agent/a.go ./internal/agent/a_test.go"

	// coverage side: the file list must yield the package dir scope. Two
	// files of the same package map to the same dir twice (matching the
	// pre-existing behavior for repeated dir args) - membership is what
	// matters, duplicates are harmless to coveragePkgInScope.
	gotCov := coverageExtractVerifyScopes("go test ./internal/agent/a.go")
	if !reflect.DeepEqual(gotCov, []string{"internal/agent"}) {
		t.Fatalf("coverageExtractVerifyScopes single file = %v, want [internal/agent]", gotCov)
	}
	for _, sc := range coverageExtractVerifyScopes(cmd) {
		if sc != "internal/agent" {
			t.Fatalf("two-file list yielded stray scope %q", sc)
		}
	}

	// debt side: same mapping (single file arg exact; two-file list only
	// requires every scope to be the package dir).
	gotDebt := parseGoPackageScopes("go test ./internal/agent/a.go")
	if !reflect.DeepEqual(gotDebt, []string{"internal/agent"}) {
		t.Fatalf("parseGoPackageScopes single file = %v, want [internal/agent]", gotDebt)
	}
	for _, sc := range parseGoPackageScopes(cmd) {
		if sc != "internal/agent" {
			t.Fatalf("debt two-file list yielded stray scope %q", sc)
		}
	}

	// End-to-end property the issue is actually about: the package named by
	// the file args must count as in-scope for coveragePkgInScope.
	if !coveragePkgInScope("internal/agent", gotCov[0]) {
		t.Fatal("coveragePkgInScope(internal/agent, scope) = false - file-list verification still misjudged UNVERIFIED")
	}

	// Bare relative file list form (no ./ prefix) maps the same way.
	for _, sc := range coverageExtractVerifyScopes("go test internal/agent/a.go internal/agent/a_test.go") {
		if sc != "internal/agent" {
			t.Fatalf("bare-relative file list yielded stray scope %q", sc)
		}
	}

	// Non-file forms are unchanged: package dir still a scope, wildcard
	// still ALL.
	if got := coverageExtractVerifyScopes("go test ./internal/agent/"); !reflect.DeepEqual(got, []string{"internal/agent"}) {
		t.Fatalf("plain package dir = %v, want [internal/agent]", got)
	}
	if got := coverageExtractVerifyScopes("go test ./..."); !reflect.DeepEqual(got, []string{"ALL"}) {
		t.Fatalf("wildcard = %v, want [ALL]", got)
	}
}
