package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/memory"
)

// MemoryRecallTool searches persistent memories (global + project scopes) by
// keyword on demand. It is the read-only replacement for unconditional
// system-prompt memory injection: entry bodies and title indexes are no
// longer injected into prompts, agents recall them through this tool instead.
type MemoryRecallTool struct {
	globalMem  *memory.AutoMemory
	projectMem *memory.AutoMemory
}

// NewMemoryRecallTool builds a memory_recall tool over the global and project
// memory stores. Either store may be nil; that scope is then skipped.
func NewMemoryRecallTool(globalMem, projectMem *memory.AutoMemory) *MemoryRecallTool {
	return &MemoryRecallTool{globalMem: globalMem, projectMem: projectMem}
}

func (t *MemoryRecallTool) Name() string { return "memory_recall" }

func (t *MemoryRecallTool) Description() string {
	return "Search persistent memories (global + project scopes) by keyword on demand - this replaces the old system-prompt memory injection " +
		"(memory bodies and title indexes are NOT auto-injected anymore, so call this whenever prior-session knowledge might exist). " +
		"Matching is case-insensitive on titles and bodies; scoring: title hit 3 pts, body hit 1 pt per query token, plus a small usage bonus. " +
		"Returns scope, key, updated time and a hit snippet per match."
}

func (t *MemoryRecallTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Keywords or substring to match against memory titles and bodies (case-insensitive, whitespace-split tokens)"
			},
			"scope": {
				"type": "string",
				"description": "Which memory scope to search: 'all' (default), 'project', or 'global'",
				"enum": ["all", "project", "global"]
			},
			"limit": {
				"type": "integer",
				"description": "Max results to return (default 8, capped at 20)"
			}
		},
		"required": ["query"]
	}`)
}

// recallDefaultLimit and recallMaxLimit bound the result window.
const (
	recallDefaultLimit = 8
	recallMaxLimit     = 20
	recallSnippetPad   = 120
	recallSnippetMax   = 300
)

// recallHit is one scored search result.
type recallHit struct {
	scope    string
	key      string
	updated  time.Time
	uses     int
	score    float64
	snippet  string
	bodyHit  bool
	titleHit bool
}

func (t *MemoryRecallTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	q, err := parseRecallParams(input)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	scopes, ok := resolveRecallScopes(t, q.scope)
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("invalid scope %q: must be 'all', 'project', or 'global'", q.scope)}, nil
	}
	hits, err := searchRecallScopes(scopes, recallTokens(q.text))
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	if len(hits) == 0 {
		return Result{Content: fmt.Sprintf("no matches for %q (scope: %s)", q.text, q.scope)}, nil
	}
	return Result{Content: renderRecallHits(rankRecallHits(hits, q.limit), q.text, q.scope, q.limit)}, nil
}

// recallQuery is a validated memory_recall request.
type recallQuery struct {
	text  string
	scope string
	limit int
}

// parseRecallParams validates the raw input and applies the defaults
// (scope all, limit 8 capped at 20). The returned error carries an
// in-band message for the caller to wrap.
func parseRecallParams(input json.RawMessage) (recallQuery, error) {
	var params struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return recallQuery{}, fmt.Errorf("invalid input: %v", err)
	}
	q := recallQuery{text: strings.TrimSpace(params.Query), scope: params.Scope, limit: params.Limit}
	if q.text == "" {
		return recallQuery{}, errors.New("query is required")
	}
	if q.scope == "" {
		q.scope = "all"
	}
	if q.limit <= 0 {
		q.limit = recallDefaultLimit
	}
	if q.limit > recallMaxLimit {
		q.limit = recallMaxLimit
	}
	return q, nil
}

// recallScope pairs a scope label with its memory store.
type recallScope struct {
	label string
	mem   *memory.AutoMemory
}

// resolveRecallScopes maps the requested scope name to the tool's stores;
// ok is false for an unknown scope name.
func resolveRecallScopes(t *MemoryRecallTool, scope string) ([]recallScope, bool) {
	switch scope {
	case "global":
		return []recallScope{{"global", t.globalMem}}, true
	case "project":
		return []recallScope{{"project", t.projectMem}}, true
	case "all":
		return []recallScope{{"project", t.projectMem}, {"global", t.globalMem}}, true
	default:
		return nil, false
	}
}

// searchRecallScopes scans every non-nil scope and returns the scored hits.
func searchRecallScopes(scopes []recallScope, tokens []string) ([]recallHit, error) {
	var hits []recallHit
	for _, s := range scopes {
		if s.mem == nil {
			continue
		}
		entries, err := s.mem.ListDetailed()
		if err != nil {
			return nil, fmt.Errorf("failed to list %s memory: %v", s.label, err)
		}
		for _, e := range entries {
			body, err := s.mem.LoadKey(e.Key)
			if err != nil {
				body = e.Preview
			}
			hit := scoreRecallEntry(e.Key, body, e.Uses, tokens)
			if hit == nil {
				continue
			}
			hit.scope = s.label
			hit.updated = e.UpdatedAt
			hit.uses = e.Uses
			hit.snippet = recallSnippet(body, tokens)
			hits = append(hits, *hit)
		}
	}
	return hits, nil
}

// rankRecallHits sorts hits by score (desc), then scope and key (asc), and
// truncates to limit.
func rankRecallHits(hits []recallHit, limit int) []recallHit {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].scope != hits[j].scope {
			return hits[i].scope < hits[j].scope
		}
		return hits[i].key < hits[j].key
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// renderRecallHits formats the ranked hits as the tool result body.
func renderRecallHits(hits []recallHit, query, scope string, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "memory_recall: %d match(es) for %q (scope: %s, limit: %d)\n", len(hits), query, scope, limit)
	for _, h := range hits {
		fmt.Fprintf(&b, "\n[%s] %s (updated %s, uses %d, score %.1f)\n%s\n",
			h.scope, h.key, h.updated.Format(time.RFC3339), h.uses, h.score, h.snippet)
	}
	return strings.TrimSpace(b.String())
}

// recallTokens lowercases and whitespace-splits the query string.
func recallTokens(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	if len(fields) == 0 {
		return []string{strings.ToLower(query)}
	}
	return fields
}

// scoreRecallEntry scores one entry against the query tokens: +3 per token
// hit in the title (key), +1 per token hit in the body, plus 0.1 per recorded
// use. It returns nil when no token matches (usage bonus alone is not a hit).
func scoreRecallEntry(key, body string, uses int, tokens []string) *recallHit {
	lowerKey := strings.ToLower(key)
	lowerBody := strings.ToLower(body)
	var score float64
	titleHit, bodyHit := false, false
	for _, tok := range tokens {
		if strings.Contains(lowerKey, tok) {
			score += 3
			titleHit = true
		}
		if strings.Contains(lowerBody, tok) {
			score++
			bodyHit = true
		}
	}
	if !titleHit && !bodyHit {
		return nil
	}
	score += 0.1 * float64(uses)
	return &recallHit{key: key, score: score, bodyHit: bodyHit, titleHit: titleHit}
}

// recallSnippet extracts a window of about +/- recallSnippetPad runes around
// the first query-token hit in body. Title-only hits fall back to the body
// head. Whitespace is collapsed and the snippet is capped at recallSnippetMax
// runes.
func recallSnippet(body string, tokens []string) string {
	collapsed := strings.Join(strings.Fields(body), " ")
	runes := []rune(collapsed)
	lower := strings.ToLower(collapsed)
	best := -1
	for _, tok := range tokens {
		if idx := strings.Index(lower, tok); idx >= 0 {
			// strings.Index returns a BYTE offset; convert to a rune index
			// before slicing the runes slice, otherwise multibyte bodies
			// (CJK) panic with slice bounds out of range (byte offset can
			// exceed the rune count).
			ridx := len([]rune(lower[:idx]))
			if best == -1 || ridx < best {
				best = ridx
			}
		}
	}
	start, end := 0, len(runes)
	if best >= 0 {
		start = best - recallSnippetPad
		if start < 0 {
			start = 0
		}
		end = best + recallSnippetPad
		if end > len(runes) {
			end = len(runes)
		}
	} else if len(runes) > 2*recallSnippetPad {
		start = 0
		end = 2 * recallSnippetPad
	}
	snippet := strings.TrimSpace(string(runes[start:end]))
	if len([]rune(snippet)) > recallSnippetMax {
		snippet = string([]rune(snippet)[:recallSnippetMax])
	}
	if start > 0 {
		snippet = "... " + snippet
	}
	if end < len(runes) {
		snippet = snippet + " ..."
	}
	if snippet == "" {
		snippet = "(empty body)"
	}
	return snippet
}
