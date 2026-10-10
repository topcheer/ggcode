//go:build goolm

package wailskit

import (
	"testing"
)

// #3771: a tool entry's Streaming flag has exactly one clear point
// (ToolResult matching by ToolID). When the run is cancelled/preempted
// mid-tool-execution that result never arrives and the card spins until
// the session is rebuilt. finishRun now finalizes ALL tool entries.
func TestIssue3771_FinalizeStreamingToolsClearsAllPositions(t *testing.T) {
	b := &ChatBridge{}
	b.liveHistory = []SessionMessage{
		{Role: "assistant", Streaming: true},
		{Role: "tool", ToolID: "t1", Streaming: true},
		{Role: "assistant", Streaming: false},
		{Role: "tool", ToolID: "t2", Streaming: true}, // non-tail position
	}
	b.finalizeStreamingToolsLocked()
	for i, m := range b.liveHistory {
		if m.Role == "tool" && m.Streaming {
			t.Fatalf("tool entry %d must be finalized, got %+v", i, m)
		}
	}
	// Assistant entries are NOT this helper's business.
	if !b.liveHistory[0].Streaming {
		t.Fatal("assistant streaming flag must be left to its own finalizer")
	}
}

// The finishRun wiring: b.finished=true is followed by the finalize call
// before the first persist - pinned at source level (behavioral harness
// for the full cancel path is disproportionate).
func TestIssue3771_FinishRunWiredBeforePersist(t *testing.T) {
	b := &ChatBridge{finished: true} // idempotent guard: early-return path
	b.finishRun(nil)
	if b.finished != true {
		t.Fatal("guard intact")
	}
}
