package audit

// Timeline returns the ledger entries for visualization, optionally filtered
// to one session (empty session = all entries). It is the entry-level
// companion of Digest/RunDigest: Digest aggregates, Timeline exposes each
// Entry (incl. Rationale/InvariantID/Peer/TaskID) for per-decision views
// such as `ggcode audit viz`.
func Timeline(path, session string) ([]Entry, error) {
	entries, err := scanEntries(path)
	if err != nil {
		return nil, err
	}
	if session == "" {
		return entries, nil
	}
	filtered := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Session == session {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}
