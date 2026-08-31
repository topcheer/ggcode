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
		// #1401-A: the old `!lok || !rok → false` made unknown ids
		// "equivalent" to everything while known ids still compared -
		// equivalence without transitivity violates strict weak ordering,
		// and under SliceStable's insertion sort an unknown element acted
		// as a barrier that known events after it could never cross (their
		// sort silently failed). Unknown ids now sort FIRST, unambiguously:
		// the production unknowns are the bootstrap events (session-info,
		// status-latest, ...) whose replay-first position is a design contract
		// (TestProjectionStoreReplayEventsCapsTailAndKeepsLatestBootstrap);
		// known ev-NNN events sort ascending after them. Strict weak ordering
		// restored: unknown < known, consistently.
		if !lok && !rok {
			return false // both unknown: stable original order
		}
		if !lok {
			return true // left unknown only: sorts before any known
		}
		if !rok {
			return false // right unknown only: known sorts after it
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
