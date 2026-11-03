package agent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	tvMinIterations = 8

	tvWarnRatio = 4.0

	tvMinFilesForWarning = 5
)

type tunnelVisionState struct {
	baseDir string

	filesTouched map[string]bool

	searchedFiles map[string]bool
	sawTestFiles  bool

	warned bool
}

func newTunnelVisionState() *tunnelVisionState {
	return &tunnelVisionState{
		filesTouched:  make(map[string]bool),
		searchedFiles: make(map[string]bool),
	}
}

func (s *tunnelVisionState) reset() {
	s.filesTouched = make(map[string]bool)
	s.searchedFiles = make(map[string]bool)
	s.sawTestFiles = false
	s.warned = false
}

var searchPathRe = regexp.MustCompile(`(?m)^\s*([^\s:]+\.[A-Za-z0-9]+):\d+`)

func extractSearchResultPaths(toolName, content string) []string {
	if content == "" {
		return nil
	}
	seen := make(map[string]bool)
	var paths []string
	for _, m := range searchPathRe.FindAllStringSubmatch(content, 50) {
		if len(m) >= 2 && !seen[m[1]] {
			seen[m[1]] = true
			paths = append(paths, m[1])
		}
	}
	if barePathTools[toolName] || lspTools[toolName] {
		for _, m := range pathOnlyRe.FindAllStringSubmatch(content, 50) {
			if len(m) >= 2 {
				p := strings.TrimSpace(m[1])
				if p != "" && !seen[p] && isValidFilePath(p) {
					seen[p] = true
					paths = append(paths, p)
				}
			}
		}
	}
	return paths
}

func (s *tunnelVisionState) normalize(path string) string {
	return normalizeCompanionPath(s.baseDir, strings.TrimSpace(path))
}

func (s *tunnelVisionState) recordFile(path string) {
	if path == "" {
		return
	}
	n := s.normalize(path)
	isTest := strings.HasSuffix(n, "_test.go")
	if isTest {
		s.sawTestFiles = true
	} else if !strings.HasSuffix(n, ".md") {
		s.filesTouched[n] = true
	}
}

func (s *tunnelVisionState) recordSearched(path string) {
	if path == "" {
		return
	}
	n := s.normalize(path)
	if strings.HasSuffix(n, ".md") {
		return
	}
	s.searchedFiles[n] = true
	if strings.HasSuffix(n, "_test.go") {
		s.sawTestFiles = true
	}
}

func (s *tunnelVisionState) check(iterations int) string {
	if s.warned {
		return ""
	}
	if iterations < tvMinIterations {
		return ""
	}

	fileCount := len(s.filesTouched)
	if fileCount == 0 {
		return ""
	}

	searchOnly := 0
	for p := range s.searchedFiles {
		if !s.filesTouched[p] {
			searchOnly++
		}
	}
	uniqueSeen := fileCount + searchOnly
	if uniqueSeen >= tvMinFilesForWarning {
		return ""
	}

	if s.sawTestFiles && fileCount > 0 && iterations < tvMinIterations*2 {
		return ""
	}

	ratio := float64(iterations) / float64(fileCount)
	if ratio < tvWarnRatio {
		return ""
	}

	s.warned = true
	debug.Log("agent", "tunnel-vision: %d iterations but only %d unique files touched (ratio %.1f)", iterations, fileCount, ratio)

	return "Scope narrowness detected: you've completed " + strconv.Itoa(iterations) +
		" iterations but interacted with only " + strconv.Itoa(fileCount) +
		" unique files. Cross-file dependencies, callers, and integration points " +
		"may be missed. Consider broadening exploration: search for references to " +
		"the symbols you've changed, check related modules, and verify your edits " +
		"don't break callers in other files."
}
