package main

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2245LockErrorBranchClearsResumeID(t *testing.T) {
	data, err := os.ReadFile("root.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, msg := range []string{
		"session lock check for %s failed",
		"is locked by another instance",
	} {
		idx := strings.Index(src, msg)
		if idx < 0 {
			t.Fatalf("branch message %q not found - root.go changed shape", msg)
		}

		window := src[idx : idx+400]
		if !strings.Contains(window, `resumeID = ""`) {
			t.Fatalf("branch %q does not clear resumeID within its body (double-writer regression)", msg)
		}
	}
}
