package agent

import "testing"

// Issue #5023: EXPECT exit=0 verification matched foresightResultFailureRe
// against the ENTIRE tool result body, so a successful grep for "panic:"/
// "fatal:"/IsError lines (searched data, not status) was falsely reported to
// the agent as "declared exit=0 but the command reported failure".

func TestIssue5023GrepHitsAreNotCommandFailure(t *testing.T) {
	// Successful grep whose entire body is made of failure-marker hits.
	content := "internal/agent/agent.go:100:\tpanic: unreachable\n" +
		"internal/tool/x.go:5:\tfatal: bad input\n" +
		"internal/y.go:9:\tIsError bool"
	if passed, detail := evaluateExpectCheck("exit=0", content, false); !passed {
		t.Fatalf("exit=0 with grep hits in DATA must pass, got violation: %s", detail)
	}
	// The opposite declaration must NOT be satisfied by data hits either.
	if passed, _ := evaluateExpectCheck("exit!=0", content, false); passed {
		t.Fatal("exit!=0 must not pass on a successful command whose data mentions panic:")
	}
}

func TestIssue5023RealFailureStillFails(t *testing.T) {
	// Real structured failure: isError=true must fail exit=0 regardless of body.
	if passed, detail := evaluateExpectCheck("exit=0", "anything", true); passed {
		t.Fatal("isError=true must fail exit=0")
	} else if detail == "" {
		t.Fatal("expected detail")
	}
	// Status envelope at the head of the output (first non-empty line).
	if passed, detail := evaluateExpectCheck("exit=0", "exit code 1: build failed\nstdout...", false); passed {
		t.Fatalf("failure marker in status envelope must fail exit=0, got: %s", detail)
	}
}

func TestIssue5023ForesightCalibrateActualOKUsesSignal(t *testing.T) {
	// Same surface via the foresight consumer: data hits are not failure.
	content := "a.go:1:\tpanic: x\nb.go:2:\ttool_result_error"
	if foresightResultFailureSignal(content, false) {
		t.Fatal("calibration must not treat grep data hits as failure (#5023)")
	}
	if !foresightResultFailureSignal("panic: runtime error", false) {
		t.Fatal("envelope panic: must be treated as failure")
	}
	if !foresightResultFailureSignal("fine", true) {
		t.Fatal("isError must be authoritative failure signal")
	}
}

func TestIssue5023EmptyLineAwareMultiLine(t *testing.T) {
	// Secondary FN: multi-line output with an explicit empty-signal line must
	// count as empty so a declared >=1_match cannot pass with zero matches.
	content := "searched 12 files\nNo matches found"
	if !foresightResultEmpty(content) {
		t.Fatal("multi-line output ending in 'No matches found' must be empty (line-aware, #5023)")
	}
	if passed, detail := evaluateExpectCheck(">=1_match", content, false); passed {
		t.Fatalf(">=1_match with zero matches must fail, got: %s", detail)
	}
	// Real matches still pass.
	if passed, detail := evaluateExpectCheck(">=1_match", "a.go:1: hit", false); !passed {
		t.Fatalf(">=1_match with hits must pass, got: %s", detail)
	}
}
