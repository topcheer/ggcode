package sessionidx

import "strings"

// Session-id validation. Session ids flow in from tool arguments (the
// session_recall `session_id` parameter) and from directory scans; they are
// joined into filesystem paths (<sessionsDir>/<id>.jsonl and
// <sessionsDir>/idx/<id>.idx). CodeQL flagged the resulting path injection
// (4 high alerts on the #5164 diff), so every id is validated at the entry
// points instead of being trusted.

const (
	// maxSessionIDLen bounds an id well above real ids
	// (YYYYMMDD-HHMMSS-<hex>) while refusing absurd inputs.
	maxSessionIDLen = 128
	// maxQueryLimit bounds a single query's result allocation: the limit
	// arrives from tool arguments, so an unbounded value is a memory
	// amplification vector.
	maxQueryLimit = 500
)

// ValidSessionID reports whether id is a safe session identifier: non-empty,
// bounded, and restricted to the character set real ids use. Path
// separators, "..", NUL and any other exotic bytes are rejected outright.
func ValidSessionID(id string) bool {
	if id == "" || len(id) > maxSessionIDLen {
		return false
	}
	if strings.Contains(id, "..") {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

// clampLimit bounds a caller-supplied result limit.
func clampLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > maxQueryLimit {
		return maxQueryLimit
	}
	return limit
}
