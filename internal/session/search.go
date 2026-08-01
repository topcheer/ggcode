package session

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"
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

	// Sort by UpdatedAt descending so the most relevant / recent sessions
	// are scanned first — important for the maxResults cutoff.
	sort.Slice(idx, func(i, j int) bool {
		return idx[i].UpdatedAt.After(idx[j].UpdatedAt)
	})

	var results []SearchResult
	for _, e := range idx {
		hits, err := searchInSessionFile(s.sessionPath(e.ID), e.Title, needle)
		if err != nil {
			continue // skip unreadable sessions
		}
		results = append(results, hits...)
		if maxResults > 0 && len(results) >= maxResults {
			results = results[:maxResults]
			break
		}
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
func searchInSessionFile(path, title, needle string) ([]SearchResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var hits []SearchResult
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Quick reject: skip lines that definitely don't contain the needle.
		// This avoids a full JSON unmarshal for the vast majority of records.
		if !strings.Contains(strings.ToLower(line), needle) {
			continue
		}

		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type != "message" || rec.Message == nil {
			continue
		}

		msg := rec.Message
		for _, block := range msg.Content {
			if block.Type != "text" {
				continue
			}
			text := block.Text
			idx := strings.Index(strings.ToLower(text), needle)
			if idx < 0 {
				continue
			}
			hits = append(hits, SearchResult{
				SessionID: extractSessionID(path),
				Title:     title,
				Role:      msg.Role,
				Snippet:   makeSnippet(text, idx, needle),
				Timestamp: rec.Timestamp,
			})
		}
	}
	return hits, sc.Err()
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
