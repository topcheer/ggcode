package agent

import (
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
	// filesTouched tracks unique normalized file paths read or edited.
	filesTouched map[string]bool

	// warned indicates the detector has fired this run.
	warned bool
}

func newTunnelVisionState() *tunnelVisionState {
	return &tunnelVisionState{
		filesTouched: make(map[string]bool),
	}
}

func (s *tunnelVisionState) reset() {
	s.filesTouched = make(map[string]bool)
	s.warned = false
}

// recordFile marks a file as touched (read or edited) during this run.
func (s *tunnelVisionState) recordFile(path string) {
	if path == "" {
		return
	}
	n := normalizePath(path)
	if !strings.HasSuffix(n, "_test.go") && !strings.HasSuffix(n, ".md") {
		s.filesTouched[n] = true
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

	// Don't fire if the agent has explored enough files.
	if fileCount >= tvMinFilesForWarning {
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
