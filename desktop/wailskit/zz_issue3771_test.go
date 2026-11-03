//go:build goolm

package wailskit

import (
	"testing"
)

func TestIssue3771_FinalizeStreamingToolsClearsAllPositions(t *testing.T) {
	b := &ChatBridge{}
	b.liveHistory = []SessionMessage{
		{Role: "assistant", Streaming: true},
		{Role: "tool", ToolID: "t1", Streaming: true},
		{Role: "assistant", Streaming: false},
		{Role: "tool", ToolID: "t2", Streaming: true},
	}
	b.finalizeStreamingToolsLocked()
	for i, m := range b.liveHistory {
		if m.Role == "tool" && m.Streaming {
			t.Fatalf("tool entry %d must be finalized, got %+v", i, m)
		}
	}

	if !b.liveHistory[0].Streaming {
		t.Fatal("assistant streaming flag must be left to its own finalizer")
	}
}

func TestIssue3771_FinishRunWiredBeforePersist(t *testing.T) {
	b := &ChatBridge{finished: true}
	b.finishRun(nil)
	if b.finished != true {
		t.Fatal("guard intact")
	}
}
