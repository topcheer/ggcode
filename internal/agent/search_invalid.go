package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	maxSearchInvalidationWarnings = 3
)

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

var pathInOutputRe = regexp.MustCompile(`(?:^|[\s\n])(\.?/?[a-zA-Z0-9_./-]+\.(?:go|ts|tsx|js|jsx|py|rs|java|rb|c|cpp|h|hpp|css|scss|html|vue|svelte|sql|sh|yaml|yml|json|toml|md)):\d+`)

var pathOnlyRe = regexp.MustCompile(`(?m)(?:^|[\s"'])(\.?/?[a-zA-Z0-9_./-]+\.(?:go|ts|tsx|js|jsx|py|rs|java|rb|c|cpp|h|hpp|css|scss|html|vue|svelte|sql|sh|yaml|yml|json|toml|md))(?:[\s"':]|$)`)

type searchInvalidationState struct {
	searchResultFiles map[string]string

	baseDir string

	invalidatedWarned map[string]bool

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

func (s *searchInvalidationState) normalize(p string) string {
	return normalizeCompanionPath(s.baseDir, normalizePath(p))
}

var lspTools = map[string]bool{
	"lsp_diagnostics":       true,
	"lsp_symbols":           true,
	"lsp_workspace_symbols": true,
	"lsp_references":        true,
}

var barePathTools = map[string]bool{
	"grep":         true,
	"search_files": true,
	"code_search":  true,
}

func (s *searchInvalidationState) recordSearchResult(toolName, output string) {
	if !searchResultTools[toolName] || output == "" {
		return
	}
	s.extractPaths(pathInOutputRe, output, toolName)
	if lspTools[toolName] || barePathTools[toolName] {
		s.extractPaths(pathOnlyRe, output, toolName)
	}
}

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

func (s *searchInvalidationState) checkEditInvalidation(path string) string {
	if path == "" {
		return ""
	}
	n := s.normalize(path)
	if n == "" {
		return ""
	}

	searchTool, found := s.searchResultFiles[n]
	if !found {
		return ""
	}

	if s.invalidatedWarned[n] {
		return ""
	}

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

func isValidFilePath(path string) bool {
	if len(path) < 3 || len(path) > 512 {
		return false
	}

	return strings.Contains(path, "/") && strings.Contains(path, ".")
}
