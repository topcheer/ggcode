package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
)

type SearchResult struct {
	SessionID string    `json:"session_id"`
	Title     string    `json:"title"`
	Role      string    `json:"role"`
	Snippet   string    `json:"snippet"`
	Timestamp time.Time `json:"timestamp"`
}

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

	sort.Slice(idx, func(i, j int) bool {
		return idx[i].UpdatedAt.After(idx[j].UpdatedAt)
	})

	var results []SearchResult
	for _, e := range idx {
		hits, err := searchInSessionFile(s.sessionPath(e.ID), e.Title, needle)
		if err != nil {
			continue
		}
		results = append(results, hits...)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	return results, nil
}

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
			continue
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" && strings.Contains(strings.ToLower(trimmed), needle) {
			if hit, ok := searchJSONLLine(trimmed, path, title, needle); ok {
				hits = append(hits, hit)
			}
		}
		if rerr != nil {

			break
		}
	}
	if skippedLong > 0 {
		debug.Log("session", "search: skipped %d over-long line(s) >10MB in %s (hits kept: %d)", skippedLong, path, len(hits))
	}
	return hits, nil
}

var errLineTooLong = errors.New("line exceeds limit")

func readLineLimited(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			if len(buf)+len(chunk) > limit {

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
		idx := indexFoldText(block.Text, needle)
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

func indexFoldText(text, needle string) int {
	if needle == "" {
		return 0
	}
	for pos := 0; pos < len(text); {
		tp, np := pos, 0
		for np < len(needle) && tp < len(text) {
			r, size := utf8.DecodeRuneInString(text[tp:])
			if r == utf8.RuneError && size <= 1 {
				break
			}
			lr := strings.ToLower(string(r))
			if np+len(lr) > len(needle) || needle[np:np+len(lr)] != lr {
				break
			}
			tp += size
			np += len(lr)
			if np == len(needle) {
				return pos
			}
		}
		_, size := utf8.DecodeRuneInString(text[pos:])
		if size <= 0 {
			break
		}
		pos += size
	}
	return -1
}

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

	snippet = strings.ReplaceAll(snippet, "\n", " ")
	snippet = strings.ReplaceAll(snippet, "\r", "")
	return snippet
}

func extractSessionID(path string) string {
	base := path
	if i := strings.LastIndexAny(base, `\/`); i >= 0 {
		base = base[i+1:]
	}
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}
