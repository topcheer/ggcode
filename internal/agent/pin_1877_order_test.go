package agent

import "testing"

func TestIrrevGateProductionOrder(t *testing.T) {

	s := newIrrevGateState()
	s.recordAction("run_command", `{"command":"go test ./..."}`)
	s.recordOutcome("run_command", false)
	s.recordAction("run_command", `{"command":"go test ./..."}`)
	s.recordOutcome("run_command", true)
	if got := s.totalGrounded; got != 1 {
		t.Fatalf("scenario 1: failed call must revoke only its own entry, totalGrounded=%d want 1", got)
	}

	s2 := newIrrevGateState()
	s2.recordAction("run_command", `{"command":"go test ./..."}`)
	s2.recordOutcome("run_command", true)
	s2.recordAction("read_file", `{"path":"a.go"}`)

	s2.recordAction("run_command", `{"command":"go build ./..."}`)
	s2.recordOutcome("run_command", true)

	if got := s2.totalGrounded; got != 1 {
		t.Fatalf("scenario 2: interleaved failed run_commands must be revoked (only the successful read stays), totalGrounded=%d want 1", got)
	}
}
