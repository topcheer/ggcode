package tunnel

import "testing"

func TestSortReplayEventsUnknownSortsFirst(t *testing.T) {
	events := []GatewayMessage{
		{EventID: "ev-000000002"},
		{EventID: "session-info"},
		{EventID: "ev-000000003"},
		{EventID: "ev-000000001"},
	}
	SortReplayEvents(events)
	if events[0].EventID != "session-info" {
		t.Fatalf("unknown bootstrap id must sort first, got order: %v", eventIDs(events))
	}
	for i, want := range []string{"ev-000000001", "ev-000000002", "ev-000000003"} {
		if events[i+1].EventID != want {
			t.Fatalf("known ids must sort ascending past the unknown: got %v", eventIDs(events))
		}
	}
}

func eventIDs(events []GatewayMessage) []string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.EventID
	}
	return ids
}
