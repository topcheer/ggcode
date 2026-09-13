package main

import (
	"os"
	"strings"
	"testing"
)

// #2245: the session-lock IO-error branch must clear resumeID, mirroring
// the real-conflict branch - the message says "Starting a new session" and
// the intent is the safe new-session fallback. Without the reset the path
// silently resumed the session with no lock held (double-writer window).
//
// resumeID is a local in RunPipe's scope, so this is a source-level pin
// (same convention as the #1644 scheduler gate pin): every branch that
// prints a "Starting a new session" message must reset resumeID before
// the consumer at the SetResumeID call site.
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
		// the reset must appear within the 400 bytes after the branch's
		// Fprintf (branch body incl. shortID handling)
		window := src[idx : idx+400]
		if !strings.Contains(window, `resumeID = ""`) {
			t.Fatalf("branch %q does not clear resumeID within its body (double-writer regression)", msg)
		}
	}
}
