package agent

import (
	"strings"
)

type conflictPair struct {
	sideA    []string
	sideB    []string
	priority string
}

var guidanceConflicts = []conflictPair{
	{

		sideA:    []string{"ACT NOW", "STOP EXPLOR", "PAUSE EXPLOR", "MAKE YOUR BEST-GUESS EDIT"},
		sideB:    []string{"EXPLOR", "BROADEN", "WIDEN SCOPE", "READ MORE", "UNDERSTAND BEFORE"},
		priority: "Resolve by acting on the highest-priority task directive; do not stall on further exploration.",
	},
	{

		sideA:    []string{"NARROW SCOPE", "NARROWER", "SCOPE-NARROW", "REDUCE SCOPE", "TOO BROAD"},
		sideB:    []string{"BROADEN", "WIDER", "BROAD SEARCH", "EXPAND SCOPE", "TOO NARROW"},
		priority: "Pick one search strategy based on task phase; do not alternate.",
	},
	{

		sideA:    []string{"REDUCE ITERAT", "SPEED UP", "FEWER CALLS", "MINIMIZE CALLS", "TOO MANY CALLS", "CALL ECONOMY"},
		sideB:    []string{"VERIFY", "BE THOROUGH", "DOUBLE-CHECK", "MORE TESTS", "COMPREHENSIVE", "DON'T SKIP"},
		priority: "Balance speed and thoroughness per the current task's risk level.",
	},
	{

		sideA:    []string{"COMMIT NOW", "STAGE AND COMMIT", "IS READY TO COMMIT"},
		sideB:    []string{"DON'T COMMIT", "NOT READY", "BEFORE COMMITTING", "VERIFY BEFORE COMMIT", "DO NOT COMMIT"},
		priority: "Do not commit until verification passes; the conservative directive wins.",
	},
}

func detectGuidanceConflict(hints []string) string {
	if len(hints) < 2 {
		return ""
	}

	upperHints := make([]string, len(hints))
	for idx, h := range hints {
		upperHints[idx] = strings.ToUpper(h)
	}

	for _, cp := range guidanceConflicts {

		for i, ui := range upperHints {
			if !containsAnyKeyword(ui, cp.sideA) || containsAnyKeyword(ui, cp.sideB) {
				continue
			}
			for j, uj := range upperHints {
				if i == j {
					continue
				}
				if containsAnyKeyword(uj, cp.sideB) && !containsAnyKeyword(uj, cp.sideA) {
					return "[guidance-conflict] Contradictory directives detected in concurrent guidance " +
						"(cross-component interference). Two retained hints push in opposite directions. " +
						cp.priority
				}
			}
		}
	}

	return ""
}

func containsAnyKeyword(s string, subs []string) bool {
	for _, sub := range subs {
		if keywordTokensAtWordStart(s, sub) {
			return true
		}
	}
	return false
}

func keywordTokensAtWordStart(s, keyword string) bool {
	if keyword == "" {
		return false
	}
	idx := 0
	for {
		j := strings.Index(s[idx:], keyword)
		if j < 0 {
			return false
		}
		at := idx + j
		if at == 0 || !isWordRune(s[at-1]) {
			return true
		}
		idx = at + 1
	}
}

func isWordRune(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}
