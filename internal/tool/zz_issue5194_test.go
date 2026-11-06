package tool

import (
	"os"
	"strings"
	"testing"
)

// #5194 B: the CommandJobFailed early return in executeWithAutoBackground
// must call cancel(), like its sibling error branches (:err, :nil-job). The
// job-path ctx is created WITHOUT defer cancel() at run_command.go's creation
// site (cancel ownership transfers to the job), so every early return in
// executeWithAutoBackground must call cancel() itself. Today job.finish()
// also fires the stored cancel, which makes this leak-free-by-coupling; this
// anchor keeps the explicit call from being dropped again.
func TestIssue5194FailedJobEarlyReturnCallsCancel(t *testing.T) {
	src, err := os.ReadFile("run_command.go")
	if err != nil {
		t.Fatalf("read run_command.go: %v", err)
	}
	text := string(src)
	marker := "snapshot != nil && snapshot.Status == CommandJobFailed"
	idx := strings.Index(text, marker)
	if idx < 0 {
		t.Fatal("run_command.go no longer contains the CommandJobFailed early-return marker (#5194 anchor drift)")
	}
	// cancel() must appear in the branch body BEFORE the branch returns its
	// error result; the return closes the block.
	body := text[idx:]
	ret := strings.Index(body, "return Result{IsError: true, Content: content}")
	if ret < 0 {
		t.Fatal("CommandJobFailed branch return anchor moved (#5194 anchor drift)")
	}
	branch := body[:ret]
	if !strings.Contains(branch, "cancel()") {
		t.Fatal("CommandJobFailed early return does not call cancel() before returning — #5194 regression")
	}
}

// Documents the invariant the issue's verification step established: the
// JobManager/GUI branch of the cmdCtx creation site has no defer cancel()
// (cancel ownership transfers to the job), which is why every early return in
// executeWithAutoBackground must call cancel() itself.
func TestIssue5194JobPathCreationSiteHasNoDeferCancel(t *testing.T) {
	src, err := os.ReadFile("run_command.go")
	if err != nil {
		t.Fatalf("read run_command.go: %v", err)
	}
	start := strings.Index(string(src), "if t.JobManager != nil || isGUI {")
	end := strings.Index(string(src), "} else {")
	if start < 0 || end < 0 || end < start {
		t.Fatal("run_command.go creation-site anchors moved (#5194 anchor drift)")
	}
	jobBranch := string(src)[start:end]
	if strings.Contains(jobBranch, "defer cancel()") {
		t.Fatal("job-path creation branch now has defer cancel() — cancel ownership changed; revisit #5194 anchors")
	}
}
