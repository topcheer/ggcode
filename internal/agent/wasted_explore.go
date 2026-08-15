package agent

// Wasted Exploration Detector
//
// Research basis: ACE (Agentic Context Engineering, ICLR 2026, arXiv 2510.04618)
// identifies "context collapse" and context waste patterns where agents consume
// context tokens on exploration that never translates into action. The memory
// management empirical study (arXiv 2505.16067) shows that agents frequently
// retrieve information but fail to act on it, wasting both compute and context.
//
// Problem: AI coding agents often call exploratory search tools (grep,
// search_files, glob, code_search, lsp_symbols, lsp_references, etc.) that
// return actionable file paths or symbol locations -- but then never read,
// edit, or otherwise act on ANY of those discovered paths. This means:
//   - Context tokens were spent on search results that went unused
//   - The agent may be exploring "just to feel busy" without a plan
//   - Critical files were discovered but overlooked
//   - The agent may re-discover the same files in later iterations
//
// Competitor analysis:
//   - Claude Code: no detection of unused search results
//   - Cursor: no tracking of exploration-to-action pipeline
//   - OpenHands: tracks tool usage but not result utilization
//   - Devin: no wasted exploration detection
//   - Aider: minimal search, relies on repo map
//
// Gap: No deterministic system tracks whether search tool results were
// actually consumed by subsequent file operations. This detector identifies
// search calls that returned file paths but where none of those paths were
// accessed within a subsequent window of iterations.
//
// Approach: When a search tool returns results containing file paths, record
// those paths. If within wastedExploreWindow iterations, none of those paths
// were read/edited/grep'd/etc., inject a targeted nudge. Zero LLM cost.
//
// Interaction with existing systems:
//   - empty_search_spiral: detects zero-result searches (complementary --
//     we detect non-zero results that were ignored)
//   - analysis_paralysis: detects overall exploration ratio (complementary --
//     we detect per-search result utilization)
//   - redundant_read_guard: detects re-reading same files (different concern)
//   - tool_diversity_gate: detects tool variety stagnation (different axis)

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// wastedExploreState tracks search tool results and whether they were acted on.
type wastedExploreState struct {
	mu sync.Mutex

	// pendingSearches maps a unique search ID → metadata about the search
	// and the file paths it discovered.
	pendingSearches map[int]*exploreInfo

	// nextID for unique search tracking within a run
	nextID int

	// currentIteration tracks the iteration for staleness calculation
	currentIteration int

	// consumedPaths tracks all file paths the agent has read/edited/grep'd
	// during this run. Used to check if search results were acted on.
	consumedPaths map[string]bool

	// injectionCount caps warnings per run
	injectionCount int

	// warned tracks search IDs already warned about
	warned map[int]bool
}

// exploreInfo holds metadata about a search and its discovered paths.
type exploreInfo struct {
	ID          int
	ToolName    string
	SearchQuery string   // pattern or query used
	FoundPaths  []string // file paths found in the result
	Iteration   int      // when the search was performed
	Consumed    bool     // whether any found path was subsequently accessed
}

const (
	// wastedExploreWindow: number of iterations after a search to wait
	// before declaring its results unacted-upon. Set to 2 so the agent
	// has one natural iteration to follow up.
	wastedExploreWindow = 2

	// wastedExploreMaxInjections: cap warnings per run to avoid flooding.
	wastedExploreMaxInjections = 2

	// wastedExploreMaxPending: cap tracked searches to bound memory.
	wastedExploreMaxPending = 15

	// wastedExploreMinPaths: only track searches that found at least
	// this many file paths. Single-path results are common and low-waste.
	wastedExploreMinPaths = 1

	// wastedExploreMaxPathsDisplay: cap paths shown in warning message.
	wastedExploreMaxPathsDisplay = 3
)

// searchToolNames defines tools whose results may contain actionable file paths.
var wastedExploreSearchTools = map[string]bool{
	"grep":                  true,
	"search_files":          true,
	"glob":                  true,
	"code_search":           true,
	"lsp_references":        true,
	"lsp_workspace_symbols": true,
	"lsp_implementation":    true,
	"lsp_incoming_calls":    true,
	"lsp_outgoing_calls":    true,
}

// consumptionToolNames defines tools that consume file paths (acting on them).
var wastedExploreConsumeTools = map[string]bool{
	"read_file":               true,
	"edit_file":               true,
	"multi_edit_file":         true,
	"write_file":              true,
	"multi_file_read":         true,
	"multi_file_edit":         true,
	"grep":                    true, // re-grep counts as consumption
	"search_files":            true,
	"code_search":             true,
	"lsp_hover":               true,
	"lsp_definition":          true,
	"lsp_diagnostics":         true,
	"lsp_symbols":             true,
	"lsp_document_highlights": true,
	"lsp_code_actions":        true,
	"lsp_rename":              true,
	"batch_replace":           true,
}

func newWastedExploreState() *wastedExploreState {
	return &wastedExploreState{
		pendingSearches: make(map[int]*exploreInfo),
		consumedPaths:   make(map[string]bool),
		warned:          make(map[int]bool),
	}
}

func (s *wastedExploreState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingSearches = make(map[int]*exploreInfo)
	s.consumedPaths = make(map[string]bool)
	s.warned = make(map[int]bool)
	s.nextID = 0
	s.currentIteration = 0
	s.injectionCount = 0
}

// recordSearchToolCall is called when a search tool returns results.
// It extracts file paths from the result and registers them for tracking.
func (s *wastedExploreState) recordSearchToolCall(toolName string, args json.RawMessage, result string, iteration int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !wastedExploreSearchTools[toolName] {
		return
	}

	paths := extractFilePathsFromResult(result)
	if len(paths) < wastedExploreMinPaths {
		return
	}

	// Evict oldest if at capacity
	if len(s.pendingSearches) >= wastedExploreMaxPending {
		var oldestID int
		var oldestIter int
		first := true
		for id, info := range s.pendingSearches {
			if first || info.Iteration < oldestIter {
				oldestID = id
				oldestIter = info.Iteration
				first = false
			}
		}
		delete(s.pendingSearches, oldestID)
	}

	s.nextID++
	query := extractSearchQuery(args, toolName)
	s.pendingSearches[s.nextID] = &exploreInfo{
		ID:          s.nextID,
		ToolName:    toolName,
		SearchQuery: query,
		FoundPaths:  paths,
		Iteration:   iteration,
	}

	// Immediately check if any paths were already consumed
	for _, p := range paths {
		if s.consumedPaths[p] {
			s.pendingSearches[s.nextID].Consumed = true
			break
		}
	}

	debug.Log("wasted-explore", "tracking %s search (id=%d) with %d paths at iteration %d", toolName, s.nextID, len(paths), iteration)
}

// recordConsumptionToolCall is called when a tool accesses specific file paths.
// It marks those paths as consumed and checks if any pending searches are satisfied.
func (s *wastedExploreState) recordConsumptionToolCall(toolName string, args json.RawMessage, result string, iteration int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !wastedExploreConsumeTools[toolName] {
		return
	}

	paths := extractFilePathsFromArgs(args, toolName)
	for _, p := range paths {
		s.consumedPaths[p] = true
	}

	// Also extract paths from result (e.g., read_file result contains the path)
	resultPaths := extractFilePathsFromResult(result)
	for _, p := range resultPaths {
		s.consumedPaths[p] = true
		s.consumedPaths[weNormalizePath(p)] = true
	}

	// Mark any pending searches whose paths are now consumed
	for _, info := range s.pendingSearches {
		if info.Consumed {
			continue
		}
		for _, foundPath := range info.FoundPaths {
			if s.consumedPaths[foundPath] || weMatchAnyConsumed(s, foundPath) {
				info.Consumed = true
				debug.Log("wasted-explore", "search id=%d consumed via %s at iteration %d", info.ID, toolName, iteration)
				break
			}
		}
	}
}

// checkWastedSearches returns a guidance message if any searches have
// unacted-upon results past the window threshold.
func (s *wastedExploreState) checkWastedSearches(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentIteration = iteration

	if len(s.pendingSearches) == 0 || s.injectionCount >= wastedExploreMaxInjections {
		return ""
	}

	var wasted []*exploreInfo
	for _, info := range s.pendingSearches {
		if info.Consumed || s.warned[info.ID] {
			continue
		}
		gap := iteration - info.Iteration
		if gap >= wastedExploreWindow {
			wasted = append(wasted, info)
		}
	}

	if len(wasted) == 0 {
		return ""
	}

	s.injectionCount++

	// Build guidance message
	var sb strings.Builder
	sb.WriteString("[Wasted Exploration] ")
	if len(wasted) == 1 {
		sb.WriteString("A previous search found files that haven't been accessed:\n")
	} else {
		sb.WriteString(fmt.Sprintf("%d previous searches found files that haven't been accessed:\n", len(wasted)))
	}

	for i, info := range wasted {
		if i >= 3 {
			sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(wasted)-3))
			break
		}
		s.warned[info.ID] = true
		sb.WriteString(fmt.Sprintf("  • %s (found %d file(s)", info.ToolName, len(info.FoundPaths)))
		if info.SearchQuery != "" {
			queryDisplay := info.SearchQuery
			if len(queryDisplay) > 50 {
				queryDisplay = queryDisplay[:50] + "..."
			}
			sb.WriteString(fmt.Sprintf(", query: \"%s\"", queryDisplay))
		}
		sb.WriteString("): ")
		shown := 0
		for _, p := range info.FoundPaths {
			if shown >= wastedExploreMaxPathsDisplay {
				sb.WriteString(fmt.Sprintf(" and %d more", len(info.FoundPaths)-shown))
				break
			}
			if shown > 0 {
				sb.WriteString(", ")
			}
			// Shorten path for display
			short := p
			if len(short) > 60 {
				short = "..." + short[len(short)-57:]
			}
			sb.WriteString(short)
			shown++
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\nConsider: read or act on these files if they're relevant to your task, ")
	sb.WriteString("or explicitly note why they're not needed. Unacted search results waste context tokens.")

	// Clean up old warned entries to prevent unbounded growth
	for id, info := range s.pendingSearches {
		if s.warned[id] && iteration-info.Iteration > wastedExploreWindow+5 {
			delete(s.pendingSearches, id)
			delete(s.warned, id)
		}
	}

	return sb.String()
}

// extractFilePathsFromResult parses a tool result string for file paths.
// It looks for common patterns in search tool outputs.
func extractFilePathsFromResult(result string) []string {
	if len(result) == 0 {
		return nil
	}

	var paths []string
	seen := make(map[string]bool)

	// Pattern 1: "path:" or "path=" prefixes common in structured results
	// Pattern 2: Lines that look like file paths (contain / and end with extension)
	// Pattern 3: Explicit file path markers like "File:" or "→"

	lines := strings.Split(result, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 || len(line) > 500 {
			continue
		}

		// Try to extract a path from the line
		path := extractPathFromLine(line)
		if path != "" && !seen[path] && looksLikeFilePath(path) {
			paths = append(paths, path)
			seen[path] = true
		}
	}

	return paths
}

// extractPathFromLine tries to extract a file path from a single line of output.
// weNormalizePath canonicalizes a path for cross-tool matching (#482).
// Search outputs and consumption-tool args use inconsistent formats:
// grep rg-branch emits "./relative", grep fallback emits "relative",
// lsp_* emits absolute paths, glob branches differ, read_file takes
// absolute-or-relative. Byte-equality never matches across those.
func weNormalizePath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	p = filepath.Clean(p)
	p = filepath.ToSlash(p)
	return p
}

// wePathsMatch reports whether a consumption path covers a found path,
// accepting the full normalized form or the base name when the base is
// distinctive (has an extension, length > 4) — "/abs/root/internal/agent/
// a.go" from lsp matches "./internal/agent/a.go" from grep without
// needing a workspace root.
func wePathsMatch(consumed, found string) bool {
	cn, fn := weNormalizePath(consumed), weNormalizePath(found)
	if cn == fn {
		return true
	}
	cb, fb := filepath.Base(cn), filepath.Base(fn)
	if cb == fb && cb != "/" && cb != "." && len(cb) > 4 && strings.Contains(cb, ".") {
		return true
	}
	return false
}

// weMatchAnyConsumed checks a found path against all consumed entries
// with normalization-aware matching (#482).
func weMatchAnyConsumed(s *wastedExploreState, found string) bool {
	for c := range s.consumedPaths {
		if wePathsMatch(c, found) {
			return true
		}
	}
	return false
}

// listNumPrefixRe matches the "N. " enumerator prefix of code_search
// output lines ("1. internal/agent/foo.go (relevance: 87%)").
var listNumPrefixRe = regexp.MustCompile(`^\s*\d+\.\s+`)

func extractPathFromLine(line string) string {
	// Common patterns in search output:
	// "/path/to/file.go:42: content"
	// "path/to/file.go:42: content"
	// "File: path/to/file.go"
	// "→ path/to/file.go"

	// Strip leading markers
	line = strings.TrimPrefix(line, "File: ")
	line = strings.TrimPrefix(line, "→ ")
	line = strings.TrimPrefix(line, "- ")
	// #482: code_search emits "N. path (relevance: X%)" — strip the
	// enumerator or extraction truncates to "N."
	line = listNumPrefixRe.ReplaceAllString(line, "")

	// Extract up to the first space or colon-line-number pattern
	// Look for the path portion before ":digits:" or end of meaningful content
	idx := 0
	for idx < len(line) {
		ch := line[idx]
		if ch == ' ' || ch == '\t' {
			break
		}
		// Stop at ":number:" pattern (line number in search results)
		if ch == ':' && idx+1 < len(line) && line[idx+1] >= '0' && line[idx+1] <= '9' {
			break
		}
		idx++
	}

	path := line[:idx]
	// Remove trailing colon
	path = strings.TrimSuffix(path, ":")

	return path
}

// looksLikeFilePath checks if a string looks like a real file path.
func looksLikeFilePath(s string) bool {
	if len(s) < 3 || len(s) > 500 {
		return false
	}
	// Must not be a URL
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	// Skip if it looks like a sentence/phrase
	if strings.Contains(s, " ") {
		return false
	}
	// Check for common file extensions
	lower := strings.ToLower(s)
	exts := []string{".go", ".py", ".ts", ".js", ".tsx", ".jsx", ".rs", ".java",
		".c", ".cpp", ".h", ".hpp", ".rb", ".php", ".swift", ".kt", ".scala",
		".json", ".yaml", ".yml", ".toml", ".xml", ".html", ".css", ".scss",
		".md", ".txt", ".sh", ".sql", ".proto", ".dart", ".vue", ".svelte"}
	hasExt := false
	for _, ext := range exts {
		if strings.HasSuffix(lower, ext) {
			hasExt = true
			break
		}
	}
	if !hasExt {
		// Also accept paths that look like directories or have no extension
		// but contain at least 2 path segments
		if strings.Count(s, "/") >= 2 {
			return true
		}
		return false
	}
	// Has extension -- require at least one path separator to distinguish
	// from bare filenames or extensions (e.g. ".go" alone is not a path)
	if strings.Contains(s, "/") {
		return true
	}
	// Accept "foo.go" style (filename with extension) since it could be a
	// relative path returned by search tools
	parts := strings.Split(s, ".")
	if len(parts) >= 2 && len(parts[0]) >= 1 {
		return true
	}
	return false
}

// extractFilePathsFromArgs extracts file paths from tool call arguments.
func extractFilePathsFromArgs(args json.RawMessage, _ string) []string {
	var raw map[string]interface{}
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil
	}
	paths := extractStringKeys(raw, "path", "file_path", "file", "directory", "source", "notebook_path", "url")
	paths = append(paths, extractArrayPaths(raw)...)
	return dedupPaths(paths)
}

// extractStringKeys collects string values for the given keys from a map.
func extractStringKeys(raw map[string]interface{}, keys ...string) []string {
	var paths []string
	for _, key := range keys {
		val, ok := raw[key]
		if !ok {
			continue
		}
		str, ok := val.(string)
		if !ok {
			continue
		}
		// Skip trivial path values that don't represent a specific file
		if (key == "path" || key == "directory") && (str == "" || str == ".") {
			continue
		}
		paths = append(paths, str)
	}
	return paths
}

// extractArrayPaths collects paths from "files" and "paths" array parameters.
func extractArrayPaths(raw map[string]interface{}) []string {
	var paths []string
	for _, key := range []string{"files", "paths"} {
		val, ok := raw[key]
		if !ok {
			continue
		}
		arr, ok := val.([]interface{})
		if !ok {
			continue
		}
		paths = append(paths, extractItemsFromArray(arr)...)
	}
	return paths
}

// extractItemsFromArray extracts path strings from array items (maps or strings).
func extractItemsFromArray(arr []interface{}) []string {
	var paths []string
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			for _, pk := range []string{"path", "file_path"} {
				if p, ok := m[pk].(string); ok {
					paths = append(paths, p)
				}
			}
		}
		if s, ok := item.(string); ok {
			paths = append(paths, s)
		}
	}
	return paths
}

// dedupPaths removes duplicate entries while preserving order.
func dedupPaths(paths []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}
func extractSearchQuery(args json.RawMessage, _ string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal(args, &raw); err != nil {
		return ""
	}

	for _, key := range []string{"pattern", "query", "glob", "selector", "expression"} {
		if val, ok := raw[key]; ok {
			if str, ok := val.(string); ok {
				return str
			}
		}
	}
	return ""
}

// --- Agent integration methods ---

func (a *Agent) recordWastedExploreToolCall(toolName string, args json.RawMessage, result string, iteration int) {
	if a.wastedExplore == nil {
		return
	}
	if wastedExploreSearchTools[toolName] {
		a.wastedExplore.recordSearchToolCall(toolName, args, result, iteration)
	}
	if wastedExploreConsumeTools[toolName] {
		a.wastedExplore.recordConsumptionToolCall(toolName, args, result, iteration)
	}
}

func (a *Agent) maybeWarnWastedExplore(iteration int) string {
	if a.wastedExplore == nil {
		return ""
	}
	msg := a.wastedExplore.checkWastedSearches(iteration)
	if msg != "" {
		debug.Log("wasted-explore", "wasted exploration warning at iteration %d", iteration)
	}
	return msg
}

func (a *Agent) resetWastedExplore() {
	if a.wastedExplore != nil {
		a.wastedExplore.reset()
	}
}
