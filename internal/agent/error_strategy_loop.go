package agent

import (
	"sort"
	"strings"
)

const (
	errStrategyThreshold   = 3
	errStrategyWindow      = 15
	maxErrStrategyWarnings = 2
)

type errCategory string

const (
	errCatFileNotFound    errCategory = "file-not-found"
	errCatOldTextMismatch errCategory = "old-text-mismatch"
	errCatTimeout         errCategory = "timeout"
	errCatPermission      errCategory = "permission"
	errCatGeneric         errCategory = "generic"
)

type errStrategyState struct {
	recentResults []bool
	recentCats    []errCategory
	catCounts     map[errCategory]int
	warningCount  int
	firedFor      map[errCategory]bool
}

func newErrStrategyState() *errStrategyState {
	return &errStrategyState{
		recentResults: make([]bool, 0, errStrategyWindow+1),
		recentCats:    make([]errCategory, 0, errStrategyWindow+1),
		catCounts:     make(map[errCategory]int),
		firedFor:      make(map[errCategory]bool),
	}
}

func (s *errStrategyState) reset() {
	s.recentResults = s.recentResults[:0]
	s.recentCats = s.recentCats[:0]
	s.catCounts = make(map[errCategory]int)
	s.warningCount = 0
	s.firedFor = make(map[errCategory]bool)
}

func classifyErrResult(resultContent string, isError bool) (errCategory, bool) {
	if !isError {

		return errCatGeneric, false
	}
	lower := strings.ToLower(resultContent)
	return classifyByContent(lower), true
}

func classifyByContent(lower string) errCategory {
	switch {
	case strings.Contains(lower, "old_text") &&
		(strings.Contains(lower, "not found") || strings.Contains(lower, "not unique") || strings.Contains(lower, "does not match")):
		return errCatOldTextMismatch
	case strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "file not found") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "cannot find") ||
		(strings.Contains(lower, "not found") && (strings.Contains(lower, "path") || strings.Contains(lower, "file"))):
		return errCatFileNotFound
	case strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "deadline exceeded"):
		return errCatTimeout
	case strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "operation not permitted"):
		return errCatPermission
	}
	return errCatGeneric
}

func (s *errStrategyState) recordResult(resultContent string, isError bool) {
	cat, isErr := classifyErrResult(resultContent, isError)
	s.pushEntry(cat, isErr)
}

func (s *errStrategyState) pushEntry(cat errCategory, isErr bool) {
	s.recentResults = append(s.recentResults, isErr)
	s.recentCats = append(s.recentCats, cat)
	if len(s.recentResults) > errStrategyWindow {
		if s.recentResults[0] {
			s.catCounts[s.recentCats[0]]--
			if s.catCounts[s.recentCats[0]] <= 0 {
				delete(s.catCounts, s.recentCats[0])
			}
		}
		s.recentResults = s.recentResults[1:]
		s.recentCats = s.recentCats[1:]
	}
	if isErr {
		s.catCounts[cat]++
	}
}

func (s *errStrategyState) checkAndWarn() string {
	if s.warningCount >= maxErrStrategyWarnings {
		return ""
	}
	type candidate struct {
		cat   errCategory
		count int
	}
	var candidates []candidate
	for c, n := range s.catCounts {
		candidates = append(candidates, candidate{c, n})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].count != candidates[j].count {
			return candidates[i].count > candidates[j].count
		}
		return candidates[i].cat < candidates[j].cat
	})
	var dominantCat errCategory
	var dominantCount int
	found := false
	for _, cand := range candidates {
		if s.firedFor[cand.cat] {
			continue
		}

		if cand.cat == errCatGeneric && cand.count < 5 {
			continue
		}
		if cand.count < errStrategyThreshold {

			break
		}
		dominantCat, dominantCount, found = cand.cat, cand.count, true
		break
	}
	if !found {
		return ""
	}
	s.firedFor[dominantCat] = true
	s.warningCount++
	return formatErrStrategyWarning(dominantCat, dominantCount)
}

func formatErrStrategyWarning(cat errCategory, count int) string {
	var label, strategy string
	switch cat {
	case errCatFileNotFound:
		label = "file-not-found"
		strategy = "Use glob or list_directory to discover actual file paths before editing. Files may have been renamed or your assumed path is wrong."
	case errCatOldTextMismatch:
		label = "old-text-mismatch"
		strategy = "Re-read the target file to get its current exact content before editing. Your cached version may be stale."
	case errCatTimeout:
		label = "timeout"
		strategy = "The operation is timing out. Reduce scope, add pagination, or split into smaller operations."
	case errCatPermission:
		label = "permission"
		strategy = "Permission errors indicate filesystem or access constraints. Verify the path is writable."
	default:
		label = "generic"
		strategy = "You are hitting multiple unrelated one-off errors. Slow down: re-read the relevant docs/files before each tool call and verify arguments before executing."
	}
	if cat == errCatGeneric {
		return "[error-strategy-loop] Detected " + errStrategyItoa(count) + " unrelated errors in recent tool calls. " +
			"Strategy change: " + strategy
	}
	return "[error-strategy-loop] Detected " + errStrategyItoa(count) + " '" + label +
		"' errors in recent tool calls. The same error category is recurring -- " +
		"your approach is not working. Strategy change: " + strategy
}

func errStrategyItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
