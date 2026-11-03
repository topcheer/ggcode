package agent

import (
	"strings"
	"sync"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
)

type queryConvergeState struct {
	mu sync.Mutex

	queries []queryRecord

	lastActionIter int

	warned bool

	warnCount int
}

type queryRecord struct {
	tokens    map[string]bool
	iteration int
	toolName  string
}

var qcSearchTools = map[string]bool{
	"grep":         true,
	"search_files": true,
	"code_search":  true,
	"web_search":   true,
}

var qcActionTools = derivedEditTools(map[string]bool{
	"run_command": true,
	"git_commit":  true,
})

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

	if len(q.queries) > 12 {
		q.queries = q.queries[len(q.queries)-12:]
	}
}

func (q *queryConvergeState) markUndelivered() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.warned = false
	if q.warnCount > 0 {
		q.warnCount--
	}
}

func (q *queryConvergeState) maybeWarn(iteration int) string {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.warnCount >= 2 {
		return ""
	}

	if len(q.queries) < 3 {
		return ""
	}

	mostRecent := q.queries[len(q.queries)-1]
	if q.lastActionIter >= 0 && q.lastActionIter >= mostRecent.iteration-2 {
		return ""
	}

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

	if avgSim < 0.35 {
		return ""
	}

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

	prefix := "[query-convergence] "
	if q.warnCount == 2 {
		prefix = "[query-convergence][escalated] still looping after prior hint; "
	}
	return prefix + qcIntToStr(len(window)) + " similar queries across " + qcIntToStr(len(iters)) + " iterations (avg similarity: " + qcFloatToStr(avgSim) + "). Try different search strategy or proceed with available context."
}

func qcExtractQuery(args string) string {

	for _, field := range []string{"\"query\"", "\"pattern\"", "\"q\""} {
		if val := extractJSONStringFieldQC(args, field); val != "" {
			return val
		}
	}
	return ""
}

func extractJSONStringFieldQC(json, field string) string {
	idx := strings.Index(json, field)
	if idx == -1 {
		return ""
	}
	rest := json[idx+len(field):]

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

func qcIsCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func qcTokenize(s string) map[string]bool {
	tokens := make(map[string]bool)
	var word strings.Builder
	var cjkRun []rune

	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		w := word.String()
		word.Reset()
		if len(w) <= 1 {
			return
		}

		switch w {
		case "the", "and", "for", "with", "that", "this", "from", "into",
			"are", "was", "not", "but", "all", "can", "has", "have",
			"will", "your", "youre", "find", "search":
			return
		}
		tokens[w] = true
	}
	flushCJK := func() {
		switch len(cjkRun) {
		case 0:
		case 1:

			tokens[string(cjkRun)] = true
		default:
			for i := 0; i+1 < len(cjkRun); i++ {
				tokens[string(cjkRun[i:i+2])] = true
			}
		}
		cjkRun = cjkRun[:0]
	}

	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
			flushCJK()
			word.WriteRune(r)
		case qcIsCJK(r):
			flushWord()
			cjkRun = append(cjkRun, r)
		default:
			flushWord()
			flushCJK()
		}
	}
	flushWord()
	flushCJK()
	return tokens
}

func qcJaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var intersection, union int

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

	whole := int(f)
	frac := int((f - float64(whole)) * 100)
	if frac < 0 {
		frac = -frac
	}
	return intToStr(whole) + "." + intToStr(frac/10) + intToStr(frac%10)
}
