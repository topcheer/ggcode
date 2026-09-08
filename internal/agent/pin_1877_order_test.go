package agent

import "testing"

// TestIrrevGateProductionOrder pins #1877: the production agent loop must
// call recordOutcome AFTER recordAction for the same tool call. The original
// #1776 wiring called recordOutcome ~350 lines earlier (before the ledger
// append), so the revoke always inspected the PREVIOUS call's entry - a
// failed run_command after a successful one wrongly revoked the success,
// and interleaving a read_file meant the failed grounding was never revoked
// at all (totalGrounded accumulated, push --force passed ungated).
// The companion state-machine tests in irrev_gate_test.go drive the designed
// order directly and can never catch a wiring regression; this test encodes
// the production-order contract.
func TestIrrevGateProductionOrder(t *testing.T) {
	// Scenario 1: success then failure, same tool. With the correct order,
	// the failed call's own entry is revoked; the earlier success stays.
	s := newIrrevGateState()
	s.recordAction("run_command", `{"command":"go test ./..."}`) // success grounding
	s.recordOutcome("run_command", false)                        // success: no-op
	s.recordAction("run_command", `{"command":"go test ./..."}`) // second grounding
	s.recordOutcome("run_command", true)                         // revoke the SECOND entry
	if got := s.totalGrounded; got != 1 {
		t.Fatalf("scenario 1: failed call must revoke only its own entry, totalGrounded=%d want 1", got)
	}

	// Scenario 2 (#1877 consequence 2): failed run_command interleaved with
	// reads must still be revoked - with the broken order the read_file
	// entry became the tail and the failure was never un-grounded.
	s2 := newIrrevGateState()
	s2.recordAction("run_command", `{"command":"go test ./..."}`)
	s2.recordOutcome("run_command", true) // revoked immediately
	s2.recordAction("read_file", `{"path":"a.go"}`)
	// A second failed run_command cycle, tail is read_file at outcome time
	// under the broken order; under the correct order its own entry is tail.
	s2.recordAction("run_command", `{"command":"go build ./..."}`)
	s2.recordOutcome("run_command", true)
	// Both failed run_commands are revoked (-2); the successful read_file
	// legitimately keeps its own grounding (+1). Under the broken order the
	// second failure was never revoked (totalGrounded would be 2).
	if got := s2.totalGrounded; got != 1 {
		t.Fatalf("scenario 2: interleaved failed run_commands must be revoked (only the successful read stays), totalGrounded=%d want 1", got)
	}
}
