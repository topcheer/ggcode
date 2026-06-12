package tunnel

import (
	"sort"
	"strconv"
	"strings"
)

// SortReplayEvents normalizes replay order to the canonical event-id sequence.
// Live delivery is already queue-ordered; persisted replay needs this normalization
// because projection/session recorders can append concurrently.
func SortReplayEvents(events []GatewayMessage) {
	sort.SliceStable(events, func(i, j int) bool {
		left, lok := replayEventOrdinal(events[i].EventID)
		right, rok := replayEventOrdinal(events[j].EventID)
		if !lok || !rok {
			return false
		}
		return left < right
	})
}

func replayEventOrdinal(eventID string) (int64, bool) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return 0, false
	}
	if idx := strings.LastIndex(eventID, "-"); idx >= 0 {
		eventID = eventID[idx+1:]
	}
	n, err := strconv.ParseInt(eventID, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
