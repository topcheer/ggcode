package a2aintrospect

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// #5635 P2: the activity face maps ring entries to events, recovering
// the real tool name and OK flag from the log line shape where a
// "tool=<name>" marker exists.
func TestActivityFromRingEntries_ParsesToolAndOK(t *testing.T) {
	entries := []debug.RingEntry{
		{Category: "agent", Message: "[agent] tool result ERROR: tool=run_command output=boom", Time: "04:00:00.001"},
		{Category: "agent", Message: "[agent] tool output guarded: tool=read_file tokens=10", Time: "04:00:00.002"},
		{Category: "ctx", Message: "[ctx] plain diagnostic line", Time: "04:00:00.003"},
	}
	events := ActivityFromRingEntries(entries)
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].Tool != "run_command" || events[0].OK {
		t.Errorf("event0 = %+v, want Tool=run_command OK=false", events[0])
	}
	if events[1].Tool != "read_file" || !events[1].OK {
		t.Errorf("event1 = %+v, want Tool=read_file OK=true", events[1])
	}
	if events[2].Tool != "ctx" || !events[2].OK {
		t.Errorf("event2 = %+v, want category fallback OK=true", events[2])
	}
	if events[0].TS != "04:00:00.001" {
		t.Errorf("TS = %q, want the ring timestamp", events[0].TS)
	}
}

// Ring read: debug.Log dual-writes into the in-memory ring, so a line
// logged now must be readable back through RingHistory and mapped by
// the activity face - no on-disk tail involved.
func TestActivityFromRing_ReadsLiveDebugRing(t *testing.T) {
	marker := fmt.Sprintf("ringtest-%d", time.Now().UnixNano())
	debug.Log("agent", "activity ring probe %s", marker)

	entries := debug.RingHistory(200, "")
	found := false
	for _, e := range entries {
		if strings.Contains(e.Message, marker) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("marker %q not found in ring history (%d entries)", marker, len(entries))
	}

	events := ActivityFromRingEntries(entries)
	hit := false
	for _, ev := range events {
		if strings.Contains(ev.Tool, "ringtest") || ev.Size == 0 {
			continue
		}
		hit = true
	}
	if !hit || len(events) == 0 {
		t.Fatalf("mapped %d events from ring, mapping appears broken", len(events))
	}
}
