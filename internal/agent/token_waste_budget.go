package agent

// Token Waste Budget Tracker
//
// Research basis:
//   - Xiao, Y.-A. et al. "Reducing Cost of LLM Agents with Trajectory Reduction."
//     arXiv:2509.23586 (Sept 2025, accepted FSE 2026).
//     Analyzes real agent trajectories and finds that 40-60% of input tokens
//     are wasted on useless, redundant, or expired information. Proposes
//     AgentDiet, which identifies and reduces such waste. The key insight:
//     waste is measurable as a *ratio* of consumed-but-unproductive tokens to
//     total tokens, and high waste ratios strongly correlate with task
//     failure and quality degradation.
//   - ACE Framework. "Context Waste Patterns in LLM Agents." ICLR 2026.
//     Catalogues patterns where context budget is consumed without productive
//     return, shortening the effective reasoning budget for useful work.
//
// Problem: ggcode has 131+ individual detectors that each fire on a specific
// waste pattern (redundant reads, expired reads, empty search spirals, tool
// errors, etc.). But NO detector measures the AGGREGATE waste ratio -- the
// proportion of the total token budget that has been consumed by unproductive
// tool results. The AgentDiet paper shows this aggregate signal is the single
// strongest predictor of trajectory failure: a trajectory spending >40% of
// its token budget on wasted content is statistically far more likely to
// fail, regardless of which individual detectors fire.
//
// Gap: No holistic "how much of your context budget is waste?" measurement.
// The trajectory_health.go synthesizer tracks behavioral dimensions (errors,
// edits, assumptions) but not TOKEN-LEVEL waste. This detector fills that gap
// by estimating the waste ratio from tool result sizes, classifying each
// result into waste categories, and firing when the aggregate ratio crosses
// the AgentDiet-validated 40% threshold.
//
// Design:
//   - Estimates token cost of each tool result (~4 chars/token heuristic).
//   - Classifies results into 4 waste categories (AgentDiet taxonomy):
//     1. error: tool returned an error (useless)
//     2. empty: tool returned empty/trivial content (useless)
//     3. redundant: duplicate read of unchanged file (redundant)
//     4. expired: read result later invalidated by edit (expired)
//   - Non-waste: successful tool results with substantive content that were
//     not later invalidated.
//   - Fires when wasteRatio = wasteTokens / totalTokens > 0.40 after
//     sufficient data (min 8K tokens accumulated, min 5 tool results).
//   - Fires at most 2 times per run (advisory, non-blocking).
//   - Zero LLM cost -- pure deterministic token estimation from result sizes.
//   - Complements trajectory_health.go (behavioral) with token-level (cost)
//     waste measurement.

import (
	"fmt"
	"strings"
	"sync"
)

const (
	// wasteRatioThreshold: AgentDiet-validated threshold. Trajectories with
	// >40% waste ratio have statistically higher failure rates.
	wasteRatioThreshold = 0.40

	// wasteMinTotalTokens: don't assess until enough data accumulates.
	// Below this, the ratio is too noisy to be meaningful.
	wasteMinTotalTokens = 8000

	// wasteMinToolResults: need at least this many results for a stable ratio.
	wasteMinToolResults = 5

	// wasteMaxWarnings: max warnings per run.
	wasteMaxWarnings = 2

	// tokensPerChar: rough heuristic for token estimation (~4 chars/token).
	tokensPerChar = 0.25

	// wasteEmptyThreshold: results with fewer chars than this are "empty".
	wasteEmptyThreshold = 50
)

// wasteCategory classifies a tool result's waste type (AgentDiet taxonomy).
type wasteCategory int

const (
	wasteNone      wasteCategory = iota // productive result
	wasteError                          // tool returned an error
	wasteEmpty                          // empty/trivial content
	wasteRedundant                      // duplicate read of unchanged content
	wasteExpired                        // later invalidated by edit
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

// tokenWasteEntry records a single tool result's estimated token cost and category.
type tokenWasteEntry struct {
	toolName string
	tokens   int
	category wasteCategory
}

// tokenWasteBudgetState tracks aggregate token waste across a run.
type tokenWasteBudgetState struct {
	mu          sync.Mutex
	entries     []tokenWasteEntry
	warnings    int
	totalTokens int
	wasteTokens int

	// categoryTotals for breakdown reporting.
	catTotals map[wasteCategory]int

	// readPaths tracks file reads for later expiration marking.
	// path → entry index in entries (to mark as expired when edited).
	readPaths map[string]int

	// readPathsMulti keeps the FULL read-index history per path (#418).
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

// estimateTokens approximates the token count of a string.
// Uses the standard ~4 chars/token heuristic.
func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	n := int(float64(len(s)) * tokensPerChar)
	if n < 1 {
		n = 1
	}
	return n
}

// recordToolResult records a tool result's token cost and waste category.
// toolName is the tool that was called.
// content is the result text.
// isError indicates if the tool returned an error.
// isRedundant indicates if this was flagged as a redundant/duplicate read.
// readPaths tracks file paths read (for later expiration when edited).
func (s *tokenWasteBudgetState) recordToolResult(toolName, content string, isError, isRedundant bool, pathsRead []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens := estimateTokens(content)
	cat := wasteNone

	switch {
	case isError:
		cat = wasteError
	case isRedundant:
		cat = wasteRedundant
	case len(strings.TrimSpace(content)) < wasteEmptyThreshold:
		// #419: short NEGATIVE results ("no matches found", "0 results",
		// clean-tree git status) are high-value exclusions, not waste — the
		// whole point of the search was to rule a hypothesis out. Only a
		// truly empty (or all-whitespace) result counts as wasteEmpty.
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

	// Track read paths for expiration marking. Store the FULL index history
	// per path (#418: the overwrite-style single index kept only the LAST
	// read — if that one was already redundant, the earlier productive read
	// was never reclassified as expired, systematically undercounting waste
	// in exactly the read-then-invalidate loops this detector targets).
	for _, p := range pathsRead {
		s.readPathsMulti[p] = append(s.readPathsMulti[p], idx)
	}
}

// isNegativeResult reports whether a short tool result is a structured
// negative/exclusion marker rather than empty output (#419).
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
		if strings.Contains(c, m) {
			return true
		}
	}
	return false
}

// markFileEdited invalidates prior read results for the edited file,
// reclassifying them from productive to expired (waste).
func (s *tokenWasteBudgetState) markFileEdited(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// #418: reclassify EVERY recorded read of this path, not just the last.
	for _, idx := range s.readPathsMulti[path] {
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
			// A redundant read becomes expired after the edit — the tokens
			// were already counted as waste; just reclassify the category
			// totals so the breakdown reflects the true end state.
			entry.category = wasteExpired
			s.catTotals[wasteRedundant] -= entry.tokens
			s.catTotals[wasteExpired] += entry.tokens
		}
	}
	delete(s.readPathsMulti, path)
	// Keep legacy map consistent in case other code reads it.
	delete(s.readPaths, path)
}

// maybeWarnTokenWaste checks the aggregate waste ratio and returns a guidance
// message if the threshold is exceeded. Returns empty string if no warning.
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
