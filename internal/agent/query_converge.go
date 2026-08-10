package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// queryConvergeState tracks whether the agent is stuck in a query
// convergence failure -- repeatedly issuing semantically similar search/grep
// queries across iterations without progressing to code action. This is
// inspired by the "Agentic Information Need" concept (Joe Barrow, 2026):
// when the agent's underlying information need cannot be satisfied by the
// available retrieval tools, it reformulates the same query endlessly.
//
// Unlike empty-search-spiral (checks empty results) or wasted-exploration
// (checks if results were used), this detector identifies the pattern where
// the agent keeps *rephrasing the same conceptual question* without ever
// reaching a point where it can act. It uses lightweight token-overlap
// similarity (Jaccard) -- zero LLM cost.
type queryConvergeState struct {
	mu sync.Mutex

	// recent queries with their iteration number
	queries []queryRecord

	// iteration of last code-modifying tool call (edit, write, etc.)
	lastActionIter int

	// whether a warning has been issued this run
	warned bool

	// total warnings issued (cap at 2 per run)
	warnCount int
}

type queryRecord struct {
	tokens    map[string]bool // tokenized query
	iteration int
	toolName  string
}

// searchTools whose arguments contain user-facing query strings.
var qcSearchTools = map[string]bool{
	"grep":         true,
	"search_files": true,
	"code_search":  true,
	"web_search":   true,
	"glob":         true,
}

// codeActionTools that indicate the agent moved beyond exploration.
var qcActionTools = map[string]bool{
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"write_file":       true,
	"multi_file_write": true,
	"run_command":      true,
	"git_commit":       true,
}

func newQueryConvergeState() *queryConvergeState {
	return &queryConvergeState{
		lastActionIter: -1,
	}
}

func (q *queryConvergeState) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queries = nil
	q.lastActionIter = -1
	q.warned = false
	q.warnCount = 0
}

// recordToolCall logs search queries and code actions.
func (q *queryConvergeState) recordToolCall(toolName, args string, iteration int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if qcActionTools[toolName] {
		q.lastActionIter = iteration
		return
	}

	if !qcSearchTools[toolName] {
		return
	}

	// Extract the query string from common argument patterns
	queryStr := qcExtractQuery(args)
	if queryStr == "" {
		return
	}

	tokens := qcTokenize(queryStr)
	if len(tokens) == 0 {
		return
	}

	q.queries = append(q.queries, queryRecord{
		tokens:    tokens,
		iteration: iteration,
		toolName:  toolName,
	})

	// Keep a bounded window
	if len(q.queries) > 12 {
		q.queries = q.queries[len(q.queries)-12:]
	}
}

// maybeWarn checks if the agent is stuck in a convergence failure loop.
// Fires when 3+ search queries in the recent window are highly similar to
// each other (>40% avg pairwise Jaccard) and no code action has followed.
func (q *queryConvergeState) maybeWarn(iteration int) string {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.warned || q.warnCount >= 2 {
		return ""
	}

	if len(q.queries) < 3 {
		return ""
	}

	// If the agent has taken a code action recently (within last 2 iterations
	// of the most recent query), it's not stuck.
	mostRecent := q.queries[len(q.queries)-1]
	if q.lastActionIter >= 0 && q.lastActionIter >= mostRecent.iteration-2 {
		return ""
	}

	// Compute average pairwise similarity across the recent window
	window := q.queries
	if len(window) > 6 {
		window = window[len(window)-6:]
	}

	var totalSim float64
	var pairCount int
	for i := 0; i < len(window); i++ {
		for j := i + 1; j < len(window); j++ {
			totalSim += qcJaccard(window[i].tokens, window[j].tokens)
			pairCount++
		}
	}
	if pairCount == 0 {
		return ""
	}
	avgSim := totalSim / float64(pairCount)

	// High similarity threshold: queries are conceptually the same question
	if avgSim < 0.35 {
		return ""
	}

	// All queries must span at least 2 different iterations (not all same turn)
	iters := make(map[int]bool)
	for _, qr := range window {
		iters[qr.iteration] = true
	}
	if len(iters) < 2 {
		return ""
	}

	q.warned = true
	q.warnCount++

	debug.Log("query-converge", "convergence failure detected: avgSim=%.2f, queries=%d, iters=%d", avgSim, len(window), len(iters))

	return "[query-convergence] " + qcIntToStr(len(window)) + " similar queries across " + qcIntToStr(len(iters)) + " iterations (avg similarity: " + qcFloatToStr(avgSim) + "). Try different search strategy or proceed with available context."
}

// qcExtractQuery pulls the query/pattern string from tool arguments JSON.
func qcExtractQuery(args string) string {
	// Try common field names
	for _, field := range []string{"\"query\"", "\"pattern\"", "\"q\""} {
		if val := extractJSONStringFieldQC(args, field); val != "" {
			return val
		}
	}
	return ""
}

// extractJSONStringFieldQC extracts a string value for a JSON key.
func extractJSONStringFieldQC(json, field string) string {
	idx := strings.Index(json, field)
	if idx == -1 {
		return ""
	}
	rest := json[idx+len(field):]
	// Skip whitespace and colon
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == ':' || rest[0] == '\t' || rest[0] == '\n') {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\\' && i+1 < len(rest) {
			i++
			continue
		}
		if rest[i] == '"' {
			return b.String()
		}
		b.WriteByte(rest[i])
	}
	return b.String()
}

// qcTokenize converts a query string into a set of lowercase tokens.
func qcTokenize(s string) map[string]bool {
	s = strings.ToLower(s)
	// Split on non-alphanumeric
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_')
	})
	tokens := make(map[string]bool, len(fields))
	for _, f := range fields {
		if len(f) <= 1 {
			continue
		}
		// Skip very common stop words
		switch f {
		case "the", "and", "for", "with", "that", "this", "from", "into",
			"are", "was", "not", "but", "all", "can", "has", "have",
			"will", "your", "youre", "find", "search":
			continue
		}
		tokens[f] = true
	}
	return tokens
}

// qcJaccard computes Jaccard similarity between two token sets.
func qcJaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var intersection, union int
	// Iterate over the smaller set
	if len(a) > len(b) {
		a, b = b, a
	}
	for tok := range a {
		if b[tok] {
			intersection++
		}
	}
	union = len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func qcIntToStr(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func qcFloatToStr(f float64) string {
	// Simple 2 decimal place formatting
	whole := int(f)
	frac := int((f - float64(whole)) * 100)
	if frac < 0 {
		frac = -frac
	}
	return intToStr(whole) + "." + intToStr(frac/10) + intToStr(frac%10)
}
