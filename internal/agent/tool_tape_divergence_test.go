package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/toolreplay"
)

// A REPLAY-mode session must end with a fidelity report next to the tape:
// hits/misses quantifies how far the replayed trajectory diverged from the
// recording (counterfactual divergence points), so debugging a replay does
// not require grepping debug logs.
func TestFinishToolTapeReplayWritesDivergenceReport(t *testing.T) {
	tapePath := filepath.Join(t.TempDir(), "s.tape.json")
	tape := toolreplay.NewTape()
	hitInput := json.RawMessage(`{"file_path":"/w/a.go"}`)
	tape.Record(toolreplay.Entry{
		ToolName:  "read_file",
		Input:     hitInput,
		InputHash: toolreplay.HashInput(hitInput),
		Result:    toolreplay.Result{Content: "contents"},
	})

	a := &Agent{toolTape: &toolTapeState{tape: tape, mode: toolTapeReplay, path: tapePath}}

	// One hit + one miss → fidelity 0.5.
	if _, _, handled := a.replayToolCall("read_file", hitInput); !handled {
		t.Fatal("expected replay handling for recorded call")
	}
	if _, _, handled := a.replayToolCall("read_file", json.RawMessage(`{"file_path":"/w/other.go"}`)); !handled {
		t.Fatal("expected replay handling (explicit miss) for unrecorded call")
	}

	a.finishToolTapeReplay()

	data, err := os.ReadFile(tapePath + ".divergence.json")
	if err != nil {
		t.Fatalf("divergence report not written: %v", err)
	}
	var report struct {
		ReplayedCalls int            `json:"replayed_calls"`
		Hits          int            `json:"hits"`
		Misses        int            `json:"misses"`
		Fidelity      float64        `json:"fidelity"`
		Divergences   []toolTapeMiss `json:"divergences"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report.ReplayedCalls != 2 || report.Hits != 1 || report.Misses != 1 {
		t.Fatalf("unexpected counters: %+v", report)
	}
	if report.Fidelity != 0.5 {
		t.Fatalf("fidelity = %v, want 0.5", report.Fidelity)
	}
	if len(report.Divergences) != 1 || report.Divergences[0].Tool != "read_file" {
		t.Fatalf("unexpected divergences: %+v", report.Divergences)
	}
}

// Off-mode (and a nil state) must stay inert: no report file, no panic.
func TestFinishToolTapeReplayOffModeIsInert(t *testing.T) {
	dir := t.TempDir()
	tapePath := filepath.Join(dir, "off.tape.json")
	a := &Agent{toolTape: &toolTapeState{tape: toolreplay.NewTape(), mode: toolTapeOff, path: tapePath}}
	a.finishToolTapeReplay()
	if _, err := os.Stat(tapePath + ".divergence.json"); !os.IsNotExist(err) {
		t.Fatalf("report written in off mode: %v", err)
	}
}
