//go:build darwin || linux

package agent

import (
	"context"
	"testing"
)

// #3242: skip-amnesty results (missing binary, exit 127, pytest exit 5)
// must carry Skipped=true so callers never bless the unverified tree as a
// last-known-good checkpoint baseline.

func TestExecuteVerifyCommand_SkipMarksSkipped(t *testing.T) {
	a := &Agent{workingDir: "."}
	result := a.executeVerifyCommand(context.Background(), "nonexistent_tool_xyz123 --flag")
	if !result.Passed {
		t.Fatalf("expected pass (skip), got errors: %v", result.Errors)
	}
	if !result.Skipped {
		t.Fatal("missing-binary skip must set Skipped=true (#3242)")
	}
}

func TestExecuteVerifyCommand_RealPassNotSkipped(t *testing.T) {
	a := &Agent{workingDir: "."}
	result := a.executeVerifyCommand(context.Background(), "true")
	if !result.Passed {
		t.Fatal("expected pass")
	}
	if result.Skipped {
		t.Fatal("a real pass must NOT be marked Skipped (would starve the baseline)")
	}
}

// The call-site contract: only !Skipped passes may reach
// lastGoodCheckpointRecordPass. This pins the guard logic without running
// the full async/sync orchestration.
func TestVerifySkipNotBlessedAsLastGood(t *testing.T) {
	a := &Agent{workingDir: "."}
	result := a.executeVerifyCommand(context.Background(), "nonexistent_tool_xyz123")
	if !result.Passed || !result.Skipped {
		t.Fatalf("precondition failed: Passed=%v Skipped=%v", result.Passed, result.Skipped)
	}
	// Mirror the call-site decision (verify.go async + sync paths):
	shouldBless := result.Passed && !result.Skipped
	if shouldBless {
		t.Fatal("skipped result must not bless the last-good baseline")
	}
	real := a.executeVerifyCommand(context.Background(), "true")
	if !(real.Passed && !real.Skipped) {
		t.Fatal("real pass should be blessable")
	}
}
