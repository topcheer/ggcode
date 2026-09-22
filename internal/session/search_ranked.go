package session

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Frontier grounding (2026 agentic-memory research):
//   - AgeMem (arXiv:2601.01885) argues memory operations — including
//     retrieval — should be exposed to the agent as first-class tool actions
//     so the model autonomously decides when to recall.
//   - Mem0 "State of AI Agent Memory 2026" reports the largest benchmark
//     gains come from multi-signal retrieval (term frequency + phrase +
//     temporal recency fused into one score) rather than raw substring grep.
//
// SearchSessionsRanked implements that retrieval signal for ggcode's own
// session transcripts. The plain substring SearchSessions (used by the TUI
// inspector panel) is intentionally left untouched.
//
// RankedResult extends SearchResult with the fused relevance score and the
// tokens that matched, so callers can explain WHY a hit ranked highly.
type RankedResult struct {
	SearchResult
	Score         float64  `json:"score"`
	MatchedTokens []string `json:"matched_tokens,omitempty"`
}

// RankedOptions tunes the ranked scan. Zero values mean "no filter".
type RankedOptions struct {
	// Role filters hits to "user" or "assistant" messages. Empty = both.
	Role string
	// SinceDays drops messages older than N days. 0 = no time filter.
	SinceDays int
	// Workspace restricts the scan to sessions recorded for this workspace
	// path. Sessions with an empty Workspace marker (legacy records) are
	// included so old local history stays recallable. Empty = all.
	Workspace string
	// Now overrides the wall clock for recency scoring (tests). Zero value
	// means time.Now().
	Now time.Time
}

// rankedStopwords are high-frequency English function words that carry no
// discriminative signal. CJK query text is tokenized per-character instead
// (single CJK characters ARE discriminative in Chinese), so no CJK entries
// are needed here.
var rankedStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"to": true, "of": true, "in": true, "on": true, "and": true,
	"or": true, "for": true, "do": true, "how": true, "what": true,
	"when": true, "why": true, "me": true, "my": true, "we": true,
	"you": true, "it": true, "this": true, "that": true, "be": true,
	"was": true, "were": true, "with": true, "at": true, "as": true,
	"by": true, "from": true, "i": true,
}

// TokenizeRankedQuery lowercases the query and splits it into discriminative
// tokens. ASCII words shorter than 2 runes are dropped; CJK text is split
// into individual characters (character-level matching works well for CJK
// recall because function particles like 的/了 are the only stopword class,
// and filtering them here would need a CJK stoplist — kept simple instead).
// Returns a deduplicated, order-preserving token list. A query made purely
// of stopwords yields nil; callers fall back to substring behavior.
func TokenizeRankedQuery(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool)
	var toks []string
	for _, f := range fields {
		if rankedStopwords[f] {
			continue
		}
		if hasCJK(f) {
			for _, r := range f {
				tok := string(r)
				if !seen[tok] {
					seen[tok] = true
					toks = append(toks, tok)
				}
			}
			continue
		}
		if len([]rune(f)) < 2 {
			continue
		}
		if !seen[f] {
			seen[f] = true
			toks = append(toks, f)
		}
	}
	return toks
}

func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// SearchSessionsRanked scans all session transcripts and returns the most
// relevant message-level hits, scored by a deterministic multi-signal fusion:
//
//	score = (Σ saturatedTF(token) × (0.5 + 0.5·coverage)
//	         + phraseBonus + titleBoost + recencyBoost) × roleMult
//
// Signals (Mem0 2026 multi-signal retrieval):
//   - coverage: fraction of query tokens present — multi-token AND-ish gate
//     (single-token queries and phrase matches always pass).
//   - saturatedTF: tf/(tf+1.5) per matched token — rewards denser matches
//     without letting one repeated token dominate.
//   - phraseBonus: exact multi-token phrase occurrence.
//   - titleBoost: session title matches all/most tokens.
//   - recencyBoost: exponential decay, ~14-day half-life (temporal signal).
//   - roleMult: user-stated facts get ×1.15 (actor-aware memory).
//
// The scan streams each JSONL file line by line (same #478 over-long-line
// handling as SearchSessions), collects all hits first, then sorts and
// truncates — mirroring the #536 "N best" guarantee.
func (s *JSONLStore) SearchSessionsRanked(query string, maxResults int, opts RankedOptions) ([]RankedResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	phrase := strings.ToLower(query)
	tokens := TokenizeRankedQuery(query)

	// Pure-stopword / single-punctuation query: nothing discriminative to
	// rank with — behave like substring search so the tool still answers.
	substrFallback := len(tokens) == 0

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	idx, err := s.loadIndex()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	var results []RankedResult
	for _, e := range idx {
		if opts.Workspace != "" && e.Workspace != "" && e.Workspace != opts.Workspace {
			continue
		}
		if opts.SinceDays > 0 && !e.UpdatedAt.IsZero() &&
			e.UpdatedAt.Before(now.AddDate(0, 0, -opts.SinceDays)) {
			// Whole session older than the window — skip the file entirely.
			continue
		}
		hits, err := rankedScanSessionFile(s.sessionPath(e.ID), e.Title, tokens, phrase, substrFallback, opts, now)
		if err != nil {
			continue // skip unreadable sessions
		}
		results = append(results, hits...)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Timestamp.After(results[j].Timestamp)
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}
	return results, nil
}

// rankedScanSessionFile streams one session JSONL file and returns ranked
// message hits.
func rankedScanSessionFile(path, title string, tokens []string, phrase string, substrFallback bool, opts RankedOptions, now time.Time) ([]RankedResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	titleLower := strings.ToLower(title)
	titleBoost := titleScore(titleLower, tokens)

	const maxLine = 10 * 1024 * 1024
	br := bufio.NewReader(f)

	var hits []RankedResult
	for {
		line, rerr := readLineLimited(br, maxLine)
		if rerr == errLineTooLong {
			continue // oversized line consumed; scan resumes at the next one
		}
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" {
			if hit, ok := rankedScanLine(trimmed, path, title, titleBoost, tokens, phrase, substrFallback, opts, now); ok {
				hits = append(hits, hit)
			}
		}
		if rerr != nil {
			break // io.EOF or I/O error: keep partial results (#478)
		}
	}
	return hits, nil
}

// rankedScanLine scores one JSONL record. It only unmarshals lines that could
// possibly match (cheap substring prefilter on the raw line, mirroring the
// existing search path — Go's encoder emits CJK as raw UTF-8, so the
// prefilter also works for Chinese text).
func rankedScanLine(line, path, title string, titleBoost float64, tokens []string, phrase string, substrFallback bool, opts RankedOptions, now time.Time) (RankedResult, bool) {
	// Cheap prefilter: at least one token (or the phrase) must appear in the
	// raw line before we pay for JSON unmarshal.
	if !substrFallback {
		if !strings.Contains(strings.ToLower(line), phrase) {
			any := false
			lineLower := strings.ToLower(line)
			for _, tok := range tokens {
				if strings.Contains(lineLower, tok) {
					any = true
					break
				}
			}
			if !any {
				return RankedResult{}, false
			}
		}
	}

	var rec jsonlRecord
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return RankedResult{}, false
	}
	if rec.Type != "message" || rec.Message == nil {
		return RankedResult{}, false
	}
	if opts.Role != "" && rec.Message.Role != opts.Role {
		return RankedResult{}, false
	}
	ts := rec.Timestamp
	if opts.SinceDays > 0 && !ts.IsZero() && ts.Before(now.AddDate(0, 0, -opts.SinceDays)) {
		return RankedResult{}, false
	}

	for _, block := range rec.Message.Content {
		if block.Type != "text" || block.Text == "" {
			continue
		}
		textLower := strings.ToLower(block.Text)

		if substrFallback {
			needle := strings.TrimSpace(phrase)
			if needle == "" {
				needle = strings.ToLower(title) // degenerate: match anything textual
			}
			if idx := strings.Index(textLower, needle); idx >= 0 {
				return RankedResult{
					SearchResult: newSearchResult(path, title, rec.Message.Role, makeSnippet(block.Text, idx, needle), ts),
					Score:        1.0, MatchedTokens: []string{needle},
				}, true
			}
			continue
		}

		phraseHit := len(tokens) > 1 && strings.Contains(textLower, phrase)
		// Per-block scoring: pick the densest text block of this message.
		var blockScore float64
		var blockMatched []string
		var bestIdx = -1
		for _, tok := range tokens {
			tf := strings.Count(textLower, tok)
			if tf == 0 {
				continue
			}
			blockMatched = append(blockMatched, tok)
			sat := float64(tf) / (float64(tf) + 1.5)
			blockScore += sat
			if idx := strings.Index(textLower, tok); bestIdx < 0 || idx < bestIdx {
				bestIdx = idx
			}
		}
		if len(blockMatched) == 0 {
			continue
		}
		coverage := float64(len(blockMatched)) / float64(len(tokens))
		// Multi-token queries must match at least half the tokens (OR gets
		// noisy fast) unless the exact phrase is present.
		if len(tokens) > 1 && coverage < 0.5 && !phraseHit {
			continue
		}
		blockScore = blockScore*(0.5+0.5*coverage) + titleBoost
		if phraseHit {
			blockScore += 2.0
			if idx := strings.Index(textLower, phrase); idx >= 0 {
				bestIdx = idx
			}
		}
		if !ts.IsZero() {
			ageDays := now.Sub(ts).Hours() / 24
			if ageDays < 0 {
				ageDays = 0
			}
			blockScore += 0.3 * math.Exp(-ageDays/14)
		}
		if rec.Message.Role == "user" {
			blockScore *= 1.15
		}
		if blockScore > 0 {
			return RankedResult{
				SearchResult: newSearchResult(path, title, rec.Message.Role, makeSnippet(block.Text, bestIdx, blockMatched[0]), ts),
				Score:        blockScore, MatchedTokens: blockMatched,
			}, true
		}
	}
	return RankedResult{}, false
}

func newSearchResult(path, title, role, snippet string, ts time.Time) SearchResult {
	return SearchResult{
		SessionID: extractSessionID(path),
		Title:     title,
		Role:      role,
		Snippet:   snippet,
		Timestamp: ts,
	}
}

// titleScore rewards sessions whose stored title matches the query tokens:
// all tokens → +1.5, at least half (min 1) → +0.5.
func titleScore(titleLower string, tokens []string) float64 {
	if len(tokens) == 0 || titleLower == "" {
		return 0
	}
	matched := 0
	for _, tok := range tokens {
		if strings.Contains(titleLower, tok) {
			matched++
		}
	}
	if matched == len(tokens) {
		return 1.5
	}
	if matched*2 >= len(tokens) {
		return 0.5
	}
	return 0
}
