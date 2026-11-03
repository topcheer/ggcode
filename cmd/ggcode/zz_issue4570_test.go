package main

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4570_ReplBuilderDetectsGitLazily(t *testing.T) {
	src, err := os.ReadFile("root.go")
	if err != nil {

		t.Fatalf("#4570 pin test cannot read root.go: %v", err)
	}
	s := string(src)

	if strings.Contains(s, "func() string { return gitStatus }") {
		t.Fatal("#4570: a builder still returns the startup gitStatus snapshot - repl sub-agents would carry stale branch/dirty state")
	}

	n := strings.Count(s, "func() string { return detectGitStatus(")
	if n < 2 {
		t.Fatalf("#4570: expected lazy detectGitStatus in both the skillTool and repl builders, found %d", n)
	}

	if strings.Contains(s, "gitStatus := detectGitStatus(") {
		t.Fatal("#4570: unused startup snapshot declaration remains")
	}
}
