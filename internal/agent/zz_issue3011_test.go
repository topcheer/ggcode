package agent

// Regression probes for #3011: makeRunnerNoopTargets must apply to BOTH the
// verify-counter reset (isVerifyCommandSegment) and the lastBuildFailed
// update (isRealTestSegment). #3005 wired the table into only one side, so
// `make clean` still reset the post-edit verify counter and suppressed the
// hint for unverified source edits.

import (
	"strings"
	"testing"
)

func TestIssue3011_NoopTargetsExcludedFromVerifyReset(t *testing.T) {
	for _, seg := range []string{
		"make clean", "make lint", "make fmt", "make deploy", "make help",
		"make build", "make info", "just list", "task list", "task clean",
		"make -C dir help", "make --file=x clean",
	} {
		if isVerifyCommandSegment(seg) {
			t.Fatalf("%q must NOT count as a verify command (would reset the post-edit counter)", seg)
		}
		if isRealTestSegment(strings.ToLower(strings.TrimSpace(seg))) {
			t.Fatalf("%q must NOT count as real test execution either (both sides share the table)", seg)
		}
	}
}

func TestIssue3011_RealTargetsStillCount(t *testing.T) {
	for _, seg := range []string{
		"make test", "make ci", "make e2e", "make verify-ci", "just test",
		"task test", "make", // bare `make` runs the first target (often build)
	} {
		if !isVerifyCommandSegment(seg) {
			t.Fatalf("%q must still count as a verify command", seg)
		}
	}
	for _, seg := range []string{"make test", "make ci", "just test", "task test"} {
		if !isRealTestSegment(strings.ToLower(strings.TrimSpace(seg))) {
			t.Fatalf("%q must still count as real test execution", seg)
		}
	}
}

func TestIssue3011_CompoundWithRealSegmentStillVerifies(t *testing.T) {
	// `make clean && go test ./...` contains a real test segment: the whole
	// command is verification and legitimately resets the counter.
	if !isVerifyCommand("make clean && go test ./...") {
		t.Fatal("compound containing `go test` must be a verify command")
	}
	if !isRealTestExecution("make clean && go test ./...") {
		t.Fatal("compound containing `go test` must count as real test execution")
	}
	// A pure no-op compound resets nothing.
	if isVerifyCommand("make clean && make lint") {
		t.Fatal("compound of only no-op targets must not be a verify command")
	}
	if isRealTestExecution("make clean && make lint") {
		t.Fatal("compound of only no-op targets must not count as real test execution")
	}
}

func TestIssue3011_HelperMatrix(t *testing.T) {
	cases := map[string]bool{
		"make clean":     true,
		"make deploy":    true,
		"task list":      true,
		"make -n test":   true, // dry-run flag: runs nothing
		"make test":      false,
		"make ci":        false,
		"just build-all": false,
		"make":           false, // no target word
		"go test ./...":  false, // not a runner
		"run make clean": false, // runner not first word
	}
	for seg, want := range cases {
		if got := runnerInvokesNoopTarget(strings.ToLower(strings.TrimSpace(seg))); got != want {
			t.Fatalf("runnerInvokesNoopTarget(%q) = %v, want %v", seg, got, want)
		}
	}
}
