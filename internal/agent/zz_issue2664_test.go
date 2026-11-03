package agent

import "testing"

func TestIssue2664_RedirectFormsDetected(t *testing.T) {
	mutating := []string{

		"go build ./... 2> build.err",
		"go build ./... 2>> build.err",
		"go build ./... 1> out.txt",
		"go build ./... &> log.txt",
		"go build ./... >& log.txt",
		"go build ./... > /dev/null 2> err.txt",

		"go build ./... 2>build.err",
		"go build ./... 1>out.txt",
		"go build ./... &>log.txt",
		"go test ./... &> session.log",
	}
	for _, cmd := range mutating {
		if !shellMutatesSources(cmd) {
			t.Errorf("shellMutatesSources(%q) = false, want true (#2664 missed redirect form)", cmd)
		}
	}
}

func TestIssue2664_RedirectFPGuards(t *testing.T) {
	nonMutating := []string{
		"go build ./... 2> /dev/null",
		"go build ./... &> /dev/null",
		"go build ./... >& /dev/null",
		"go build ./... 2>> /dev/null",
		"go build ./... 2>&1",
		"go build ./... > /dev/null 2>&1",
		"go build ./... 2> log",
		"grep '2> note' readme",
	}
	for _, cmd := range nonMutating {
		if shellMutatesSources(cmd) {
			t.Errorf("shellMutatesSources(%q) = true, want false (FP on non-mutating form)", cmd)
		}
	}
}
