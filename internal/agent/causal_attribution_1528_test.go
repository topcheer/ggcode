package agent

import "testing"

// #1528 case C: a SUCCEEDED read-only command whose output merely
// contains "FAIL" (grep/cat of logs) must not be attributed as a
// build/test failure - the shell bypassed the layer-1 tool-name filter.
func Test1528ReadCommandBypassGuard(t *testing.T) {
	if !looksLikeReadCommand("grep -rn FAIL ./...") {
		t.Fatal("plain grep must be recognized")
	}
	if !looksLikeReadCommand("cat ci.log") {
		t.Fatal("cat must be recognized")
	}
	if !looksLikeReadCommand("cd /tmp && grep FAIL x") {
		t.Fatal("compound (&&) must look through to the final read")
	}
	if !looksLikeReadCommand("go test ./... | grep FAIL") {
		t.Fatal("pipeline tail read must be recognized")
	}
	if looksLikeReadCommand("go build ./...") {
		t.Fatal("build command must NOT be classified as read-only")
	}
	if looksLikeReadCommand("npm test") {
		t.Fatal("test command must NOT be classified as read-only")
	}

	// Command-level: succeeded grep -> no attribution regardless of output.
	s := newCausalAttributionState()
	s.recordEdit("edit_file", "pkg/a.go", 1)
	output := "--- FAIL: TestX\npkg/a.go:12: boom\nFAIL\n"
	if got := s.attributeFailureCmd(output, "grep -rn FAIL ./...", false); got != "" {
		t.Fatalf("succeeded grep must not attribute, got %q", got)
	}
	// Errored command still attributes (exit failure = real signal).
	s2 := newCausalAttributionState()
	s2.recordEdit("edit_file", "pkg/a.go", 1)
	if got := s2.attributeFailureCmd(output, "go test ./...", true); got == "" {
		t.Fatal("errored real test command with file evidence must attribute")
	}
}
