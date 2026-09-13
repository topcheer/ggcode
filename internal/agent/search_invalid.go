package agent

// Search Result Invalidation Detector
//
// Research basis: "Reducing Cost of LLM Agents with Trajectory Reduction"
// (AgentDiet, arXiv:2509.23586, FSE 2026) identifies EXPIRED information as
// tool output that was valid when produced but invalidated by subsequent
// actions. The paper shows 39.9%-59.7% of input tokens in agent trajectories
// are waste.
//
// Existing coverage (expired_read_check.go) handles the simplest case:
// read_file(A) then edit_file(A) marks the read content as stale.
//
// THIS detector fills the orthogonal gap: search results, grep output,
// lsp_diagnostics, code_search, and workspace_symbols all return file paths
// and line numbers that become invalid after edits to those files. The agent
// frequently references stale line numbers or symbol positions from these
// results after modifying the referenced files, leading to edit failures and
// wasted iterations.
//
// Common failure pattern:
//   1. Agent runs grep/search_files -> results reference file A:42, file B:17
//   2. Agent edits file A (line numbers shift, content changes)
//   3. Agent continues referencing the old search results' line numbers
//      from file A - causing edit_file old_text mismatches or incorrect
//      code references
//
// Design:
//   - Zero LLM cost - pure path matching + map lookups
//   - Records file paths extracted from search-type tool results
//   - On subsequent file edits, checks if the edited file appeared in any
//     prior search result and marks those results as expired
//   - Fires at most maxSearchInvalidationWarnings per run (advisory)
//   - Non-blocking: hint appended to edit result

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// maxSearchInvalidationWarnings caps total notices per run.
	maxSearchInvalidationWarnings = 3
)

// searchTools lists tool names whose output contains file path references
// that become stale after edits.
var searchResultTools = map[string]bool{
	"grep":                  true,
	"search_files":          true,
	"code_search":           true,
	"lsp_diagnostics":       true,
	"lsp_references":        true,
	"lsp_definition":        true,
	"lsp_workspace_symbols": true,
	"lsp_symbols":           true,
	"lsp_implementation":    true,
}

// pathInOutputRe extracts file paths from tool output lines.
// Matches patterns like:
//
//	/path/to/file.go:42: ...
//	./pkg/file.go:17: content
//	path/to/file.go
var pathInOutputRe = regexp.MustCompile(`(?:^|[\s\n])(\.?/?[a-zA-Z0-9_./-]+\.(?:go|ts|tsx|js|jsx|py|rs|java|rb|c|cpp|h|hpp|css|scss|html|vue|svelte|sql|sh|yaml|yml|json|toml|md)):\d+`)

// pathOnlyRe extracts bare file paths without line numbers (for lsp_diagnostics
// and lsp_symbols which may output paths differently, and #1491-A: grep
// files_with_matches / code_search default output). (?m) anchors ^ at line
// starts so CONSECUTIVE bare-path lines all match - without it the shared \n
// prefix was consumed by the previous match and only every other line landed.
var pathOnlyRe = regexp.MustCompile(`(?m)(?:^|[\s"'])(\.?/?[a-zA-Z0-9_./-]+\.(?:go|ts|tsx|js|jsx|py|rs|java|rb|c|cpp|h|hpp|css|scss|html|vue|svelte|sql|sh|yaml|yml|json|toml|md))(?:[\s"':]|$)`)

// searchInvalidationState tracks search results that may become stale.
type searchInvalidationState struct {
	// searchResultFiles maps normalized file paths to the tool name that found them.
	searchResultFiles map[string]string

	// baseDir anchors relative-vs-absolute path normalization (#1491-A
	// layer 3): search-side keys are often relative (bare grep output
	// lines) while edit-side keys are absolute - without anchoring the
	// two never met and the map was dead.
	baseDir string

	// invalidatedWarned tracks file paths for which the invalidation notice
	// has already fired (avoids nagging).
	invalidatedWarned map[string]bool

	// warningCount is the total notices issued this run.
	warningCount int
}

func newSearchInvalidationState() *searchInvalidationState {
	return &searchInvalidationState{
		searchResultFiles: make(map[string]string),
		invalidatedWarned: make(map[string]bool),
	}
}

func (s *searchInvalidationState) reset() {
	s.searchResultFiles = make(map[string]string)
	s.invalidatedWarned = make(map[string]bool)
	s.warningCount = 0
}

func (s *searchInvalidationState) setBaseDir(dir string) {
	s.baseDir = dir
}

// normalize anchors the path to the workspace root (same semantics as
// unreadEditState.normalize / normalizeCompanionPath, #1559-C family):
// a relative search hit and an absolute edit target land on ONE key.
func (s *searchInvalidationState) normalize(p string) string {
	return normalizeCompanionPath(s.baseDir, normalizePath(p))
}

// lspTools lists tools that may output bare paths (no line:col prefix).
var lspTools = map[string]bool{
	"lsp_diagnostics":       true,
	"lsp_symbols":           true,
	"lsp_workspace_symbols": true,
	"lsp_references":        true,
}

// barePathTools are tools whose DEFAULT output format carries bare paths
// with no `:N` suffix (#1491-A layer 2): grep files_with_matches lists one
// bare path per line, code_search prints "N. path (relevance: %d%%)". The
// old pathInOutputRe-only extraction matched neither, so even enqueued
// results yielded zero tracked files.
var barePathTools = map[string]bool{
	"grep":         true,
	"search_files": true,
	"code_search":  true,
}

// recordSearchResult is called when a search-type tool completes. It extracts
// file paths from the tool output and tracks them for later invalidation.
func (s *searchInvalidationState) recordSearchResult(toolName, output string) {
	if !searchResultTools[toolName] || output == "" {
		return
	}
	s.extractPaths(pathInOutputRe, output, toolName)
	if lspTools[toolName] || barePathTools[toolName] {
		s.extractPaths(pathOnlyRe, output, toolName)
	}
}

// extractPaths runs a regex over output and registers first-seen paths.
func (s *searchInvalidationState) extractPaths(re *regexp.Regexp, output, toolName string) {
	matches := re.FindAllStringSubmatch(output, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		n := s.normalize(strings.TrimSpace(m[1]))
		if n == "" || !isValidFilePath(n) {
			continue
		}
		if _, exists := s.searchResultFiles[n]; !exists {
			s.searchResultFiles[n] = toolName
		}
	}
}

// checkEditInvalidation is called when the agent edits a file. If the edited
// file appeared in any prior search result, it returns a warning that those
// results may now be stale.
func (s *searchInvalidationState) checkEditInvalidation(path string) string {
	if path == "" {
		return ""
	}
	n := s.normalize(path)
	if n == "" {
		return ""
	}

	// Did any prior search result reference this file?
	searchTool, found := s.searchResultFiles[n]
	if !found {
		return ""
	}

	// Already warned about this file.
	if s.invalidatedWarned[n] {
		return ""
	}

	// Cap total warnings.
	if s.warningCount >= maxSearchInvalidationWarnings {
		return ""
	}

	s.invalidatedWarned[n] = true
	s.warningCount++

	debug.Log("agent", "search-invalidation: %s appeared in %s results and is now edited (search results are stale)", n, searchTool)

	return fmt.Sprintf(
		`[Search result invalidation] %s appeared in earlier %s results -- `+
			`those results (line numbers, symbol positions, diagnostic locations) `+
			`are now STALE due to this edit. Do not reference old line numbers or `+
			`positions from those results; re-run the search if you need current locations.`,
		path, searchTool,
	)
}

// isValidFilePath performs a basic sanity check to avoid tracking junk.
func isValidFilePath(path string) bool {
	if len(path) < 3 || len(path) > 512 {
		return false
	}
	// Must contain a file extension and at least one path separator.
	return strings.Contains(path, "/") && strings.Contains(path, ".")
}
