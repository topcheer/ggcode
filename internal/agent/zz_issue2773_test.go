package agent

import (
	"reflect"
	"testing"
)

func TestIssue2773GoFileListArgsMapToPackageScopes(t *testing.T) {
	cmd := "go test ./internal/agent/a.go ./internal/agent/a_test.go"

	gotCov := coverageExtractVerifyScopes("go test ./internal/agent/a.go")
	if !reflect.DeepEqual(gotCov, []string{"internal/agent"}) {
		t.Fatalf("coverageExtractVerifyScopes single file = %v, want [internal/agent]", gotCov)
	}
	for _, sc := range coverageExtractVerifyScopes(cmd) {
		if sc != "internal/agent" {
			t.Fatalf("two-file list yielded stray scope %q", sc)
		}
	}

	gotDebt := parseGoPackageScopes("go test ./internal/agent/a.go")
	if !reflect.DeepEqual(gotDebt, []string{"internal/agent"}) {
		t.Fatalf("parseGoPackageScopes single file = %v, want [internal/agent]", gotDebt)
	}
	for _, sc := range parseGoPackageScopes(cmd) {
		if sc != "internal/agent" {
			t.Fatalf("debt two-file list yielded stray scope %q", sc)
		}
	}

	if !coveragePkgInScope("internal/agent", gotCov[0]) {
		t.Fatal("coveragePkgInScope(internal/agent, scope) = false - file-list verification still misjudged UNVERIFIED")
	}

	for _, sc := range coverageExtractVerifyScopes("go test internal/agent/a.go internal/agent/a_test.go") {
		if sc != "internal/agent" {
			t.Fatalf("bare-relative file list yielded stray scope %q", sc)
		}
	}

	if got := coverageExtractVerifyScopes("go test ./internal/agent/"); !reflect.DeepEqual(got, []string{"internal/agent"}) {
		t.Fatalf("plain package dir = %v, want [internal/agent]", got)
	}
	if got := coverageExtractVerifyScopes("go test ./..."); !reflect.DeepEqual(got, []string{"ALL"}) {
		t.Fatalf("wildcard = %v, want [ALL]", got)
	}
}
