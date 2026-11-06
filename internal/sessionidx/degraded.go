package sessionidx

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// #5156 QA correction: a corrupt sibling .idx must degrade to a direct
// source-JSONL scan instead of silently making that session unsearchable.
// Load() already returns an empty Index for a corrupt file (comment says
// "rebuilding on refresh"), but Refresh is only ever called on the
// instance's OWN live session - the shared daemon never rebuilds a
// sibling's index (it must not write others' artifacts). So the daemon
// marks the entry degraded and serves that session from the source file
// (read-only, bounded, no index writes - the "never index inactive
// sessions" ownership contract holds).

// isCorruptIdx reports whether a loaded index is a placeholder for an
// UNREADABLE idx file (gob decode failed). It deliberately does NOT treat
// a format-version mismatch as corrupt: after an idxVersion bump every
// sibling file is stale, and degrading them all to source scans would be
// a silent full-scan storm - stale indexes are simply skipped until the
// owning instance rebuilds them.
func isCorruptIdx(ix *Index, idxSize int64) bool {
	if ix == nil || idxSize <= 0 {
		return false
	}
	return ix.corrupt
}

// scanSourceForTerms linearly scans a session's JSONL source for records
// matching ALL terms (case-insensitive substring, AND semantics - an
// approximation of the trigram path, deliberately conservative: it can
// only return records the index would also match). Bounded by limit and
// by the same per-line size gate as indexLine. Read-only: never writes.
func scanSourceForTerms(sessionsDir, sessionID string, terms []string, limit int) []Result {
	// CodeQL path-injection guard (same class as Load).
	if !ValidSessionID(sessionID) {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	lower := make([]string, 0, len(terms))
	for _, t := range terms {
		if s := strings.ToLower(strings.TrimSpace(t)); s != "" {
			lower = append(lower, s)
		}
	}
	if len(lower) == 0 {
		return nil
	}
	f, err := os.Open(filepath.Join(sessionsDir, sessionID+".jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()

	const maxIndexable = 8 << 20
	var out []Result
	br := bufio.NewReaderSize(f, 1<<20)
	for len(out) < limit {
		line, rerr := br.ReadBytes('\n')
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) > 0 && len(trimmed) <= maxIndexable &&
			bytes.Contains(trimmed, []byte(`"type":"message"`)) {
			if rec, ok := decodeMessageRecord(trimmed); ok {
				joined := strings.ToLower(strings.Join(rec.Texts, " "))
				all := true
				for _, t := range lower {
					if !strings.Contains(joined, t) {
						all = false
						break
					}
				}
				if all {
					ts := rec.TsNano
					if ts == 0 {
						ts = time.Now().UnixNano()
					}
					out = append(out, Result{
						SessionID: sessionID,
						Role:      roleToByte(rec.Role),
						TsNano:    ts,
						Line:      string(trimmed),
					})
				}
			}
		}
		if rerr != nil {
			break // EOF or read error: serve what we have
		}
	}
	return out
}
