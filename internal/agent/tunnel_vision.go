package agent

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Tunnel Vision Detector
//
// Research basis: Coppersun.dev's 2026 "AI Coding Assistant Blind Spots" study
// identifies that agents develop structural tunnel vision: "The context window
// holds 1-2 files; taint flows cross 3+." Agents edit/read the same 1-2 files
// repeatedly without exploring related files, missing cross-file dependencies
// and integration points.
//
// The "How Coding Agents Fail Their Users" study (arXiv:2605.29442) documents
// that agents routinely narrow scope to a single file after initial exploration,
// never returning to verify cross-file impact — even when their edits create
// symbols referenced elsewhere.
//
// Unlike File Churn Detector (repeated edits to same file = broken assumptions),
// Tunnel Vision tracks the EXPLORATION BREADTH: whether the agent has looked
// at enough files to understand the surrounding context. An agent editing one
// file 5 times has file churn; an agent working across 15 iterations but only
// touching 2 files has tunnel vision.
//
// Unlike Analysis Paralysis (too much exploration, too little action), Tunnel
// Vision is the opposite failure: too LITTLE exploration breadth relative to
// the work's scope. Both are failure modes, but in opposite directions.
//
// Design:
//   - Tracks unique files touched (read or edited) per run.
//   - Fires when iterations are high relative to files touched, indicating
//     the agent is cycling within a narrow set rather than exploring context.
//   - Non-blocking: advisory guidance appended to tool result.
//   - Fires at most once per run.
//   - Zero LLM cost — pure heuristic.

const (
	// tvMinIterations is the minimum iterations before tunnel vision can fire.
	// Below this, the agent hasn't had enough cycles to demonstrate narrowing.
	tvMinIterations = 8

	// tvWarnRatio is the iterations-to-files ratio that triggers a warning.
	// If the agent has done N iterations but only touched < N/ratio files,
	// it's cycling within too narrow a scope. 4.0 means the agent has done
	// 4x more iterations than files explored.
	tvWarnRatio = 4.0

	// tvMinFilesForWarning is the absolute max files touched that still
	// qualifies as tunnel vision. If the agent explored 8+ files, it has
	// reasonable breadth even if the ratio is high.
	tvMinFilesForWarning = 5
)

type tunnelVisionState struct {
	// baseDir anchors relative paths so an absolute read and a relative
	// grep hit on the same file land on one map key (#2976 companion
	// finding). Empty means no anchoring (unit tests use bare paths).
	baseDir string

	// filesTouched tracks unique normalized file paths read or edited.
	filesTouched map[string]bool

	// searchedFiles tracks files seen via search tools (#476).
	searchedFiles map[string]bool
	// sawTestFiles reports whether any _test.go file was touched — a
	// test-fix task legitimately revolves around test files (#476).
	sawTestFiles bool

	// warned indicates the detector has fired this run.
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

// searchPathRe extracts file paths from search-tool result text. Tool
// output formats here all lead lines with a path (grep: "file.go:12:...",
// LSP: absolute or relative paths in diagnostic lists). The tool set is
// search_invalid.go's searchResultTools (reused, #476).
var searchPathRe = regexp.MustCompile(`(?m)^\s*([^\s:]+\.[A-Za-z0-9]+):\d+`)

// extractSearchResultPaths pulls unique file paths from search output.
func extractSearchResultPaths(content string) []string {
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
	return paths
}

// normalize anchors the path to baseDir before keying (#2976 companion
// finding): an absolute read ("/repo/a.go") and a relative grep hit
// ("a.go") previously split into two keys, undercounting even the union.
// Mirrors unreadEditState.normalize semantics via normalizeCompanionPath.
func (s *tunnelVisionState) normalize(path string) string {
	return normalizeCompanionPath(s.baseDir, strings.TrimSpace(path))
}

// recordFile marks a file as touched (read or edited) during this run.
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

// recordSearched marks a file as SEEN via search-tool output (#476) —
// grep content/files_with_matches, code_search, lsp_references, etc.
// Search-driven exploration is breadth: a 12-file grep sweep is broad
// exploration even when the agent only read_file's 2 cores. Counted in a
// separate set so read/edit weight stays authoritative but breadth
// blindness is gone.
func (s *tunnelVisionState) recordSearched(path string) {
	if path == "" {
		return
	}
	n := s.normalize(path)
	if strings.HasSuffix(n, ".md") {
		return // docs never indicate code exploration breadth
	}
	s.searchedFiles[n] = true
	if strings.HasSuffix(n, "_test.go") {
		s.sawTestFiles = true
	}
}

// check returns a non-empty guidance message if the agent is showing tunnel
// vision — high iteration count relative to unique files explored.
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

	// #476: search-driven breadth counts toward the exploration ceiling.
	// If the agent has SEEN enough unique files across read+search, it is
	// not tunnel-visioned regardless of the read-only ratio.
	// #2976: UNION, not sum - a file that was read (filesTouched) AND shows
	// up in search hits (searchedFiles) is one file seen, not two. Summing
	// double-counted the overlap, and grep hitting files the agent is
	// actively editing is the NORM, not an edge case: an agent reading 3
	// files with iterations ratio ~5 and a grep whose hits are exactly those
	// 3 files summed to 6 >= tvMinFilesForWarning and suppressed the warning
	// this detector exists to fire.
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

	// #476: a test-fix task (agent actively editing/reading _test.go files)
	// legitimately revolves around few files — ratio alone must not fire.
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
