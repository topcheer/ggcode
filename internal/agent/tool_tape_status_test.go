package agent

import "testing"

// ToolTapeStatus probes: three-state mapping and nil-safety. The live
// entry count is covered indirectly by the replay/record tape tests in
// tool_tape_test.go; here we pin the /tape status contract.
func TestToolTapeStatusStates(t *testing.T) {
	// nil state -> off/""/0 without panicking.
	a := &Agent{}
	if mode, path, entries := a.ToolTapeStatus(); mode != "off" || path != "" || entries != 0 {
		t.Fatalf("nil state must report off, got %q %q %d", mode, path, entries)
	}
	// off state.
	a = &Agent{toolTape: newToolTapeState()}
	if mode, _, _ := a.ToolTapeStatus(); mode != "off" {
		t.Fatalf("default state must be off, got %q", mode)
	}
}

func TestToolTapeStatusRecordMode(t *testing.T) {
	t.Setenv("GGCODE_TOOL_TAPE", "record:/tmp/status-probe.tape.json")
	a := &Agent{toolTape: newToolTapeState()}
	mode, path, entries := a.ToolTapeStatus()
	if mode != "record" || path != "/tmp/status-probe.tape.json" || entries != 0 {
		t.Fatalf("record state mismatch: %q %q %d", mode, path, entries)
	}
}
