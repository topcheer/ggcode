package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// SearchResult represents a single message-level hit from a cross-session search.
type SearchResult struct {
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Role      string    `json:"role"`    // "user" or "assistant"
	Snippet   string    `json:"snippet"` // up to 200 chars of context around the match
	Timestamp time.Time `json:"timestamp"`
}

// SearchSessions scans the message content of all sessions and returns hits
// matching the query (case-insensitive substring). Each session is scanned at
// most once, and only message-type JSONL records are inspected — usage,
// metric, and meta records are skipped for speed.
//
// The search is optimized for the common case (tens to low-hundreds of
// sessions). It streams each JSONL file line by line without fully loading
// sessions into memory, and stops after maxResults hits (0 = unlimited).
func (s *JSONLStore) SearchSessions(query string, maxResults int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	needle := strings.ToLower(query)

	s.mu.Lock()
	idx, err := s.loadIndex()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	// Sort by UpdatedAt descending so the most recently active sessions are
	// scanned first — a scan-order tiebreak only, since results are globally
	// sorted by message timestamp before truncation below.
	sort.Slice(idx, func(i, j int) bool {
		return idx[i].UpdatedAt.After(idx[j].UpdatedAt)
	})

	// #536: collect ALL hits first, then sort by message timestamp, then
	// truncate. Previously the scan stopped once maxResults hits were
	// accumulated, so older hits from early-scanned (recently-updated)
	// sessions evicted newer hits from later-scanned sessions — the final
	// result was not "the N most recent matches".
	var results []SearchResult
	for _, e := range idx {
		hits, err := searchInSessionFile(s.sessionPath(e.ID), e.Title, needle)
		if err != nil {
			continue // skip unreadable sessions
		}
		results = append(results, hits...)
	}

	// Sort results by timestamp descending (most recent first).
	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	return results, nil
}

// searchInSessionFile scans a single JSONL session file for message records
// whose text content contains the (already lowercased) needle.
//
// #478: uses bufio.Reader (not Scanner) so a single over-long JSONL line
// (e.g. a multi-MB base64 image blob — same class as #291) only discards
// THAT line: previously bufio.Scanner's 10MB cap aborted the whole scan,
// and the caller's error-continue silently dropped every hit already
// collected plus everything after, with no log.
func searchInSessionFile(path, title, needle string) ([]SearchResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	const maxLine = 10 * 1024 * 1024
	br := bufio.NewReader(f)

	var hits []SearchResult
	skippedLong := 0
	for {
		line, rerr := readLineLimited(br, maxLine)
		if rerr == errLineTooLong {
			skippedLong++
			continue // oversized line consumed; scan resumes at the next one
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" && strings.Contains(strings.ToLower(trimmed), needle) {
			if hit, ok := searchJSONLLine(trimmed, path, title, needle); ok {
				hits = append(hits, hit)
			}
		}
		if rerr != nil {
			// io.EOF (or a real I/O error): keep what we have — partial
			// results beat none (#478).
			break
		}
	}
	if skippedLong > 0 {
		debug.Log("session", "search: skipped %d over-long line(s) >10MB in %s (hits kept: %d)", skippedLong, path, len(hits))
	}
	return hits, nil
}

// errLineTooLong signals readLineLimited consumed and discarded an
// over-long line; the reader is positioned at the start of the next one.
var errLineTooLong = errors.New("line exceeds limit")

// readLineLimited reads one line up to limit bytes. If the line is longer,
// it consumes and discards the remainder (through the newline) and returns
// (nil, errLineTooLong) so the caller can continue with the next line.
func readLineLimited(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			if len(buf)+len(chunk) > limit {
				// Discard this entire line, keep the reader moving.
				for err == bufio.ErrBufferFull {
					chunk, err = br.ReadSlice('\n')
				}
				return nil, errLineTooLong
			}
			buf = append(buf, chunk...)
			continue
		}
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, errLineTooLong
		}
		return buf, err
	}
}

// searchJSONLLine unmarshals one JSONL line and returns a match if any
// text block contains the needle.
func searchJSONLLine(line, path, title, needle string) (SearchResult, bool) {
	var rec jsonlRecord
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return SearchResult{}, false
	}
	if rec.Type != "message" || rec.Message == nil {
		return SearchResult{}, false
	}
	for _, block := range rec.Message.Content {
		if block.Type != "text" {
			continue
		}
		idx := strings.Index(strings.ToLower(block.Text), needle)
		if idx < 0 {
			continue
		}
		return SearchResult{
			SessionID: extractSessionID(path),
			Title:     title,
			Role:      rec.Message.Role,
			Snippet:   makeSnippet(block.Text, idx, needle),
			Timestamp: rec.Timestamp,
		}, true
	}
	return SearchResult{}, false
}

// makeSnippet extracts up to 200 characters of context centered on the match.
func makeSnippet(text string, matchIdx int, needle string) string {
	const maxSnippet = 200
	half := (maxSnippet - len(needle)) / 2
	if half < 0 {
		half = 0
	}

	start := matchIdx - half
	if start < 0 {
		start = 0
	}
	end := matchIdx + len(needle) + half
	if end > len(text) {
		end = len(text)
	}

	snippet := strings.TrimSpace(text[start:end])
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(text) {
		snippet = snippet + "..."
	}

	// Normalize newlines for single-line display.
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	snippet = strings.ReplaceAll(snippet, "\r", "")
	return snippet
}

// extractSessionID derives the session ID from a JSONL file path.
func extractSessionID(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, "."); i >= 0 {
		base = base[:i]
	}
	return base
}
