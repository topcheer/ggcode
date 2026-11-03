package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	wasteRatioThreshold = 0.40

	wasteMinTotalTokens = 8000

	wasteMinToolResults = 5

	wasteMaxWarnings = 2

	tokensPerChar = 0.25

	wasteEmptyThreshold = 50
)

type wasteCategory int

const (
	wasteNone wasteCategory = iota
	wasteError
	wasteEmpty
	wasteRedundant
	wasteExpired
)

func (wc wasteCategory) String() string {
	switch wc {
	case wasteError:
		return "error"
	case wasteEmpty:
		return "empty"
	case wasteRedundant:
		return "redundant"
	case wasteExpired:
		return "expired"
	default:
		return "productive"
	}
}

type tokenWasteEntry struct {
	toolName string
	tokens   int
	category wasteCategory
}

type tokenWasteBudgetState struct {
	mu          sync.Mutex
	entries     []tokenWasteEntry
	warnings    int
	totalTokens int
	wasteTokens int

	catTotals map[wasteCategory]int

	readPaths map[string]int

	readPathsMulti map[string][]int
}

func newTokenWasteBudgetState() *tokenWasteBudgetState {
	return &tokenWasteBudgetState{
		catTotals:      make(map[wasteCategory]int),
		readPaths:      make(map[string]int),
		readPathsMulti: make(map[string][]int),
	}
}

func (s *tokenWasteBudgetState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	s.warnings = 0
	s.totalTokens = 0
	s.wasteTokens = 0
	s.catTotals = make(map[wasteCategory]int)
	s.readPaths = make(map[string]int)
	s.readPathsMulti = make(map[string][]int)
}

func estimateTokens(s string) int {
	return estimateTokensLen(s, len(s))
}

func estimateTokensLen(s string, length int) int {
	if length < 0 {
		length = len(s)
	}
	if length == 0 {
		return 0
	}
	n := int(float64(length) * tokensPerChar)
	if n < 1 {
		n = 1
	}
	return n
}

func (s *tokenWasteBudgetState) recordToolResult(toolName, content string, isError, isRedundant bool, pathsRead []string) {
	s.recordToolResultLen(toolName, content, -1, isError, isRedundant, pathsRead)
}

func (s *tokenWasteBudgetState) recordToolResultLen(toolName, content string, originalLen int, isError, isRedundant bool, pathsRead []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens := estimateTokensLen(content, originalLen)
	cat := wasteNone

	switch {
	case isError:
		cat = wasteError
	case isRedundant:
		cat = wasteRedundant
	case len(strings.TrimSpace(content)) < wasteEmptyThreshold:

		if !isNegativeResult(content) {
			cat = wasteEmpty
		}
	}

	entry := tokenWasteEntry{
		toolName: toolName,
		tokens:   tokens,
		category: cat,
	}
	idx := len(s.entries)
	s.entries = append(s.entries, entry)
	s.totalTokens += tokens
	if cat != wasteNone {
		s.wasteTokens += tokens
		s.catTotals[cat] += tokens
	}

	for _, p := range pathsRead {
		k := wastePathKey(p)
		s.readPathsMulti[k] = append(s.readPathsMulti[k], idx)
	}
}

func isNegativeResult(content string) bool {
	c := strings.ToLower(strings.TrimSpace(content))
	if c == "" {
		return false
	}
	negativeMarkers := []string{
		"no match", "no results", "no result", "0 results", "0 matches",
		"no entries found", "nothing found", "not found", "no changes",
		"nothing to", "no such", "clean", "up to date", "already up-to-date",
		"0 findings", "no findings", "no issues",
	}
	for _, m := range negativeMarkers {

		if keywordTokensAtWordStart(c, m) {
			return true
		}
	}
	return false
}

func (s *tokenWasteBudgetState) markFileEdited(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := wastePathKey(path)

	for _, idx := range s.readPathsMulti[key] {
		if idx >= len(s.entries) {
			continue
		}
		entry := &s.entries[idx]
		switch entry.category {
		case wasteNone:
			entry.category = wasteExpired
			s.wasteTokens += entry.tokens
			s.catTotals[wasteExpired] += entry.tokens
		case wasteRedundant:

			entry.category = wasteExpired
			s.catTotals[wasteRedundant] -= entry.tokens
			s.catTotals[wasteExpired] += entry.tokens
		}
	}
	delete(s.readPathsMulti, key)

	delete(s.readPaths, key)
}

func wastePathKey(p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		if wd, err := os.Getwd(); err == nil {
			if abs, aerr := filepath.Abs(filepath.Join(wd, p)); aerr == nil {
				p = abs
			}
		}
	}
	return filepath.Clean(p)
}

func (a *Agent) maybeWarnTokenWaste() string {
	if a.tokenWasteBudget == nil {
		return ""
	}
	s := a.tokenWasteBudget
	s.mu.Lock()
	if s.warnings >= wasteMaxWarnings {
		s.mu.Unlock()
		return ""
	}
	if s.totalTokens < wasteMinTotalTokens || len(s.entries) < wasteMinToolResults {
		s.mu.Unlock()
		return ""
	}
	ratio := float64(s.wasteTokens) / float64(s.totalTokens)
	if ratio <= wasteRatioThreshold {
		s.mu.Unlock()
		return ""
	}
	s.warnings++
	breakdown := ""
	for _, cat := range []wasteCategory{wasteError, wasteEmpty, wasteRedundant, wasteExpired} {
		if tokens, ok := s.catTotals[cat]; ok && tokens > 0 {
			if breakdown != "" {
				breakdown += ", "
			}
			breakdown += fmt.Sprintf("%s=%d", cat, tokens)
		}
	}
	totalTokens := s.totalTokens
	wasteTokens := s.wasteTokens
	s.mu.Unlock()

	return fmt.Sprintf(
		"[token-waste] %.0f%% of your tool-result context budget (%d/%d est tokens) "+
			"is consumed by wasted content [%s]. Research (AgentDiet, arXiv:2509.23586, "+
			"FSE 2026) shows trajectories above 40%% waste have significantly higher failure "+
			"rates. Reduce waste by: (1) avoiding re-reading files you already have in context, "+
			"(2) using targeted offset/limit reads instead of full-file reads, "+
			"(3) not repeating failed tool calls without changing arguments, "+
			"(4) batch-reading files you need once with multi_file_read.\n",
		ratio*100, wasteTokens, totalTokens, breakdown,
	)
}
