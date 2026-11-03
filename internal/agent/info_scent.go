package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

type infoScentState struct {
	mu sync.Mutex

	allSeenPaths map[string]bool

	recentExplorations []scentEntry

	injectionCount int

	lastWarnIteration int
}

type scentEntry struct {
	iteration    int
	toolName     string
	pathsFound   []string
	novelPaths   int
	totalPaths   int
	noveltyRatio float64
}

const (
	scentWindowSize     = 4
	scentDecayThreshold = 0.25
	scentMinConsecutive = 3
	scentMaxInjections  = 1
	scentMinPaths       = 2
)

func newInfoScentState() *infoScentState {
	return &infoScentState{
		allSeenPaths:       make(map[string]bool),
		recentExplorations: make([]scentEntry, 0, scentWindowSize+1),
		lastWarnIteration:  -1,
	}
}

func (s *infoScentState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allSeenPaths = make(map[string]bool)
	s.recentExplorations = make([]scentEntry, 0, scentWindowSize+1)
	s.injectionCount = 0
	s.lastWarnIteration = -1
}

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

func (s *infoScentState) recordExploration(toolName, toolArgs, resultContent string, iteration int) {
	if !scentExplorationTools[toolName] {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	paths := extractScentPaths(toolName, toolArgs, resultContent)
	if len(paths) < scentMinPaths {
		return
	}

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

	s.recentExplorations = append(s.recentExplorations, entry)
	if len(s.recentExplorations) > scentWindowSize {
		s.recentExplorations = s.recentExplorations[1:]
	}

	debug.Log("info-scent", "iter=%d tool=%s paths=%d novel=%d ratio=%.2f window=%d",
		iteration, toolName, len(paths), novel, ratio, len(s.recentExplorations))
}

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

	var tools []string
	for _, e := range s.recentExplorations[len(s.recentExplorations)-consecutiveLow:] {
		tools = append(tools, fmt.Sprintf("%s (%d/%d novel)", e.toolName, e.novelPaths, e.totalPaths))
	}

	debug.Log("info-scent", "scent decay detected at iteration %d: %d consecutive low-novelty explorations",
		iteration, consecutiveLow)

	return fmt.Sprintf(
		"[info-scent] Last %d exploration calls mostly revisited known paths (%s). Act on existing info or search new areas.",
		consecutiveLow, strings.Join(tools, ", "))

}

func extractScentPaths(toolName, args, result string) []string {
	pathSet := make(map[string]bool)

	for _, p := range extractFilePathsFromJSON(args) {
		if p != "" {
			pathSet[p] = true
		}
	}

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

var scentPathRe = regexp.MustCompile(`(?:^|["'\s(/\[])((?:\.{0,2}/)?[\w./-]+\.[a-zA-Z]{1,10})`)

func extractFilePathsFromJSON(argsJSON string) []string {
	if argsJSON == "" {
		return nil
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {

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
