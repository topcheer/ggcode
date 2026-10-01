package agent

import (
	"strings"
	"testing"
)

// #3045 B1: a pure formatter (gofmt) must satisfy the HINT-suppression
// flag but NOT the final-gate evidence flag.
func TestIssue3045_B1_FormatterDoesNotSatisfyGate(t *testing.T) {
	if !isVerifyCommand("gofmt -l .") {
		t.Fatal("sanity: gofmt is a verify-family command (hint suppression)")
	}
	if isRealTestExecution("gofmt -l .") {
		t.Fatal("gofmt must not count as a real test/build execution")
	}
	if isRealTestExecution("prettier --check .") {
		t.Fatal("prettier must not count as a real test/build execution")
	}
	if !isRealTestExecution("go test ./...") {
		t.Fatal("sanity: go test is a real execution")
	}
	// The gate itself takes the flag, so the flag semantics above carry the
	// acceptance: a run whose only verify was `gofmt -l` must still gate.
}

// #3045 B2: Go VCS-path dependencies (dotted-domain first segment) are
// git-URL resolvable - no registry-squatting surface - so a fresh go.mod
// full of github.com/... deps must not eat the slopsquatting warning.
func TestIssue3045_B2_GoVCSPathsExempt(t *testing.T) {
	w := checkNewDependencyVerify("go.mod",
		"",
		"module example.com/x\n\ngo 1.27\n\nrequire (\n\tgithub.com/spf13/cobra v1.8.0\n\tgolang.org/x/sync v0.7.0\n)\n")
	if len(w) != 0 {
		t.Fatalf("VCS-path Go deps must be exempt, got %v", w)
	}
	// Registry-style short name in npm still warns (the real attack surface).
	w = checkNewDependencyVerify("package.json", `{}`,
		`{ "dependencies": { "react-pro-memo-hooks": "^1.0.0" } }`)
	if len(w) != 1 || !strings.Contains(w[0], "slopsquatting") {
		t.Fatalf("npm short-name hallucination must still warn, got %v", w)
	}
}

func TestIsVCSModulePath(t *testing.T) {
	for _, p := range []string{"github.com/a/b", "gopkg.in/yaml.v3", "golang.org/x/sync"} {
		if !isVCSModulePath(p) {
			t.Errorf("%s should be VCS path", p)
		}
	}
	for _, p := range []string{"react-pro-memo-hooks", "cobra", "gocloud.dev"} {
		// note: gocloud.dev IS dotted-domain - single-element module paths
		// like it are VCS too, which is the correct semantics.
		if p == "gocloud.dev" && !isVCSModulePath(p) {
			t.Errorf("%s should be VCS path", p)
		}
		if (p == "react-pro-memo-hooks" || p == "cobra") && isVCSModulePath(p) {
			t.Errorf("%s should NOT be VCS path", p)
		}
	}
}
