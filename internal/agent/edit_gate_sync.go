package agent

// derivedEditTools returns a new set containing the canonical
// sourceMutatingTools superset (#153/#154) plus the given extra tools.
//
// Per-purpose mutation gates whose semantics are exactly "modifies source
// files on disk" should reference sourceMutatingTools directly (alias or
// predicate) instead of maintaining their own table. Gates with a WIDER
// semantic (e.g. also treating git side effects or command execution as
// "mutating") must be built with this helper so the canonical file-editing
// members can never drift out of sync again (issue #738).
func derivedEditTools(extra map[string]bool) map[string]bool {
	m := make(map[string]bool, len(sourceMutatingTools)+len(extra))
	for t := range sourceMutatingTools {
		m[t] = true
	}
	for t := range extra {
		m[t] = true
	}
	return m
}
