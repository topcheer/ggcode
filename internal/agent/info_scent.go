package agent

// Information Scent Decay Detector
//
// Research basis: Information Foraging Theory applied to LLM agents
// (InForage, ICLR 2025, arXiv 2505.09316; Pirolli & Card 1999).
// Agents follow "information scent" cues when searching for relevant code.
// When consecutive search/read operations yield decreasing novel information
// (e.g., returning the same files, empty results, or already-seen content),
// the agent is foraging in a depleted information patch.
//
// The InForage framework shows that RL-trained agents optimize for information
// gain per action. Production agents without this signal waste context tokens
// on low-yield searches in the same code region, when they should either:
//   - Move to a different "patch" (different file/directory/module)
//   - Switch from exploration to action (start editing)
//   - Ask the user for guidance
//
// Problem: No deterministic system tracks whether each successive read/search
// is yielding NEW information or just re-discovering known content. Agents
// get stuck reading the same files with different tools, or searching with
// slightly different queries that return overlapping results.
//
// Detection approach:
//   - For each search/read tool call, extract the set of file paths touched
//   - Compute the ratio of novel paths (not seen in prior calls)
//   - If 3+ consecutive exploration calls yield <25% novel paths, the
//     information scent has decayed -- the agent is in a depleted patch
//   - Inject a targeted nudge to pivot strategy
//
// Interaction with existing systems:
//   - wasted_explore: detects search results never acted upon (complementary --
//     we detect diminishing returns even when results ARE used)
//   - redundant_read_guard: detects re-reading identical files (different --
//     we track novelty ratio across different files in the same region)
//   - query_converge: detects similar queries (different -- we track result
//     overlap, not query similarity)
//   - tunnel_vision: detects narrow scope (complementary -- we detect the
//     information depletion that causes tunnel vision)

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// infoScentState tracks information novelty across consecutive exploration calls.
type infoScentState struct {
	mu sync.Mutex

	// allSeenPaths accumulates file paths discovered/read across the entire run
	allSeenPaths map[string]bool

	// recentExplorations tracks the last N exploration tool calls with their
	// novelty ratio, to detect a sustained decay trend
	recentExplorations []scentEntry

	// injectionCount caps warnings per run
	injectionCount int

	// lastWarnIteration prevents consecutive-injection noise
	lastWarnIteration int
}

// scentEntry records a single exploration call's information yield.
type scentEntry struct {
	iteration    int
	toolName     string
	pathsFound   []string
	novelPaths   int
	totalPaths   int
	noveltyRatio float64
}

const (
	scentWindowSize     = 4    // track last 4 exploration calls
	scentDecayThreshold = 0.25 // <25% novel paths = depleted
	scentMinConsecutive = 3    // need 3+ consecutive low-novelty calls
	scentMaxInjections  = 1    // max warnings per run
	scentMinPaths       = 2    // ignore calls with <2 paths (noise)
)

// newInfoScentState creates a fresh information scent tracker.
func newInfoScentState() *infoScentState {
	return &infoScentState{
		allSeenPaths:       make(map[string]bool),
		recentExplorations: make([]scentEntry, 0, scentWindowSize+1),
		lastWarnIteration:  -1,
	}
}

// reset clears all state for a new run.
func (s *infoScentState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allSeenPaths = make(map[string]bool)
	s.recentExplorations = make([]scentEntry, 0, scentWindowSize+1)
	s.injectionCount = 0
	s.lastWarnIteration = -1
}

// scentExplorationTools are tools that search for information (not modify).
var scentExplorationTools = map[string]bool{
	"read_file":             true,
	"grep":                  true,
	"search_files":          true,
	"glob":                  true,
	"code_search":           true,
	"list_directory":        true,
	"lsp_symbols":           true,
	"lsp_references":        true,
	"lsp_workspace_symbols": true,
	"lsp_definition":        true,
	"lsp_implementation":    true,
	"lsp_hover":             true,
	"multi_file_read":       true,
	"git_blame":             true,
	"git_show":              true,
	"git_log":               true,
}

// recordExploration records an exploration tool call and its discovered paths.
// Returns true if the call was tracked (i.e., it was an exploration tool).
func (s *infoScentState) recordExploration(toolName, toolArgs, resultContent string, iteration int) {
	if !scentExplorationTools[toolName] {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Extract file paths from the tool arguments and result content
	paths := extractScentPaths(toolName, toolArgs, resultContent)
	if len(paths) < scentMinPaths {
		return
	}

	// Compute novelty: how many paths haven't been seen before
	novel := 0
	for _, p := range paths {
		if !s.allSeenPaths[p] {
			novel++
			s.allSeenPaths[p] = true
		}
	}

	ratio := float64(novel) / float64(len(paths))
	entry := scentEntry{
		iteration:    iteration,
		toolName:     toolName,
		pathsFound:   paths,
		novelPaths:   novel,
		totalPaths:   len(paths),
		noveltyRatio: ratio,
	}

	// Append to recent window
	s.recentExplorations = append(s.recentExplorations, entry)
	if len(s.recentExplorations) > scentWindowSize {
		s.recentExplorations = s.recentExplorations[1:]
	}

	debug.Log("info-scent", "iter=%d tool=%s paths=%d novel=%d ratio=%.2f window=%d",
		iteration, toolName, len(paths), novel, ratio, len(s.recentExplorations))
}

// maybeWarn checks if information scent has decayed and returns a nudge if so.
func (s *infoScentState) maybeWarn(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.injectionCount >= scentMaxInjections {
		return ""
	}
	if s.lastWarnIteration >= 0 && iteration-s.lastWarnIteration < 4 {
		return ""
	}
	if len(s.recentExplorations) < scentMinConsecutive {
		return ""
	}

	// Check if the last scentMinConsecutive entries all have low novelty
	consecutiveLow := 0
	for i := len(s.recentExplorations) - 1; i >= 0; i-- {
		e := s.recentExplorations[i]
		if e.noveltyRatio <= scentDecayThreshold && e.totalPaths >= scentMinPaths {
			consecutiveLow++
		} else {
			break
		}
	}

	if consecutiveLow < scentMinConsecutive {
		return ""
	}

	s.injectionCount++
	s.lastWarnIteration = iteration

	// Build the warning
	var tools []string
	for _, e := range s.recentExplorations[len(s.recentExplorations)-consecutiveLow:] {
		tools = append(tools, fmt.Sprintf("%s (%d/%d novel)", e.toolName, e.novelPaths, e.totalPaths))
	}

	debug.Log("info-scent", "scent decay detected at iteration %d: %d consecutive low-novelty explorations",
		iteration, consecutiveLow)

	return fmt.Sprintf(
		"[info-scent] Last %d exploration calls mostly revisited known paths (%s). Act on existing info or search new areas.",
		consecutiveLow, strings.Join(tools, ", "))
	// Note: tools slice already joined, no format injection risk
}

// extractScentPaths extracts file paths from tool arguments and results.
func extractScentPaths(toolName, args, result string) []string {
	pathSet := make(map[string]bool)

	// Extract from arguments (read_file path, glob pattern, grep path)
	for _, p := range extractFilePathsFromJSON(args) {
		if p != "" {
			pathSet[p] = true
		}
	}

	// Extract from result content (search results contain file paths)
	for _, p := range extractFilePathsFromText(result) {
		if p != "" {
			pathSet[p] = true
		}
	}

	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	return paths
}

// scentPathRe matches file paths in JSON/text output.
var scentPathRe = regexp.MustCompile(`(?:^|["'\s(/\[])((?:\.{0,2}/)?[\w./-]+\.[a-zA-Z]{1,10})`)

// extractFilePathsFromJSON extracts file paths from JSON tool arguments.
func extractFilePathsFromJSON(argsJSON string) []string {
	if argsJSON == "" {
		return nil
	}

	// Try to parse as JSON and look for "path", "file", "pattern", "directory" fields
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		// Fall back to regex extraction from raw text
		return extractFilePathsFromText(argsJSON)
	}

	var paths []string
	for _, key := range []string{"path", "file", "files", "pattern", "directory", "url"} {
		if v, ok := m[key]; ok {
			switch val := v.(type) {
			case string:
				if looksLikeFilePath(val) {
					paths = append(paths, val)
				}
			case []any:
				for _, item := range val {
					if s, ok := item.(string); ok && looksLikeFilePath(s) {
						paths = append(paths, s)
					}
				}
			}
		}
	}
	return paths
}

// extractFilePathsFromText extracts file paths from arbitrary text content.
func extractFilePathsFromText(text string) []string {
	if text == "" {
		return nil
	}
	matches := scentPathRe.FindAllStringSubmatch(text, -1)
	seen := make(map[string]bool)
	var paths []string
	for _, m := range matches {
		p := strings.TrimSpace(m[1])
		if p != "" && looksLikeFilePath(p) && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return paths
}
