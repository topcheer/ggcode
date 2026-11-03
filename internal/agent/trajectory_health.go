package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	trajectoryHealthThreshold = 5

	trajectoryHealthMaxWarnings = 2

	trajectoryHealthMinIterations = 5
)

type healthDimension struct {
	name        string
	score       int
	description string
}

type trajectoryHealthState struct {
	warnings       int
	totalEdits     int
	totalErrors    int
	totalTools     int
	totalReadTools int
	iterations     int
	assumptionHits int
}

func newTrajectoryHealthState() *trajectoryHealthState {
	return &trajectoryHealthState{}
}

func (s *trajectoryHealthState) reset() {
	*s = trajectoryHealthState{}
}

func (s *trajectoryHealthState) recordIteration(editCount, errorCount, toolCount, readCount, assumptionCount int) {
	s.iterations++
	s.totalEdits += editCount
	s.totalErrors += errorCount
	s.totalTools += toolCount
	s.totalReadTools += readCount
	s.assumptionHits += assumptionCount
}

func (s *trajectoryHealthState) assess() ([]healthDimension, int) {
	var dims []healthDimension

	if s.iterations >= 3 {
		editRatio := float64(s.totalEdits) / float64(s.iterations)
		if editRatio > 3.0 {
			dims = append(dims, healthDimension{"edit-volume", 2,
				fmt.Sprintf("high edit volume (%d edits / %d iter = %.1fx)", s.totalEdits, s.iterations, editRatio)})
		} else if editRatio > 2.0 {
			dims = append(dims, healthDimension{"edit-volume", 1,
				fmt.Sprintf("elevated edit volume (%d edits / %d iter = %.1fx)", s.totalEdits, s.iterations, editRatio)})
		}
	}

	if s.iterations >= 3 {
		errRatio := float64(s.totalErrors) / float64(s.iterations)
		if errRatio > 1.5 {
			dims = append(dims, healthDimension{"error-load", 2,
				fmt.Sprintf("high error load (%d errors / %d iter = %.1f)", s.totalErrors, s.iterations, errRatio)})
		} else if errRatio > 0.7 {
			dims = append(dims, healthDimension{"error-load", 1,
				fmt.Sprintf("moderate error load (%d errors / %d iter = %.1f)", s.totalErrors, s.iterations, errRatio)})
		}
	}

	if s.iterations >= 3 && s.totalTools >= 6 {
		failRate := float64(s.totalErrors) / float64(s.totalTools)
		if failRate > 0.3 {
			dims = append(dims, healthDimension{"tool-failure", 2,
				fmt.Sprintf("high tool failure rate (%d/%d = %.0f%%)", s.totalErrors, s.totalTools, failRate*100)})
		} else if failRate > 0.15 {
			dims = append(dims, healthDimension{"tool-failure", 1,
				fmt.Sprintf("moderate tool failure rate (%d/%d = %.0f%%)", s.totalErrors, s.totalTools, failRate*100)})
		}
	}

	if s.iterations >= 3 && s.totalTools >= 6 {
		readRatio := float64(s.totalReadTools) / float64(s.totalTools)
		if readRatio > 0.7 {
			dims = append(dims, healthDimension{"explore-heavy", 2,
				fmt.Sprintf("exploration-heavy trajectory (%d/%d = %.0f%% read-only tools)", s.totalReadTools, s.totalTools, readRatio*100)})
		} else if readRatio > 0.5 {
			dims = append(dims, healthDimension{"explore-heavy", 1,
				fmt.Sprintf("moderate exploration bias (%d/%d = %.0f%% read-only tools)", s.totalReadTools, s.totalTools, readRatio*100)})
		}
	}

	if s.iterations >= 3 {
		assumeRatio := float64(s.assumptionHits) / float64(s.iterations)
		if assumeRatio > 1.0 {
			dims = append(dims, healthDimension{"assumption-load", 2,
				fmt.Sprintf("high assumption density (%d signals / %d iter = %.1f)", s.assumptionHits, s.iterations, assumeRatio)})
		} else if assumeRatio > 0.5 {
			dims = append(dims, healthDimension{"assumption-load", 1,
				fmt.Sprintf("moderate assumption density (%d signals / %d iter = %.1f)", s.assumptionHits, s.iterations, assumeRatio)})
		}
	}

	total := 0
	for _, d := range dims {
		total += d.score
	}
	return dims, total
}

func countToolTypes(toolCalls []provider.ToolCallDelta) (editCount, readCount int) {
	for _, tc := range toolCalls {
		if sourceMutatingTools[tc.Name] {
			editCount++
			continue
		}
		switch tc.Name {
		case "read_file", "multi_file_read", "grep", "search_files", "glob",
			"list_directory", "code_search", "lsp_hover", "lsp_definition",
			"lsp_references", "lsp_symbols", "lsp_workspace_symbols",
			"lsp_diagnostics", "git_log", "git_status", "git_diff", "git_blame":
			readCount++
		}
	}
	return
}

func (a *Agent) maybeWarnTrajectoryHealth() string {
	if a.trajectoryHealth == nil {
		return ""
	}
	if a.trajectoryHealth.warnings >= trajectoryHealthMaxWarnings {
		return ""
	}
	if a.trajectoryHealth.iterations < trajectoryHealthMinIterations {
		return ""
	}

	dims, total := a.trajectoryHealth.assess()
	if total < trajectoryHealthThreshold {
		return ""
	}

	a.trajectoryHealth.warnings++

	var lines []string
	for _, d := range dims {
		severity := "degraded"
		if d.score >= 2 {
			severity = "critical"
		}
		lines = append(lines, fmt.Sprintf("  [%s/%s] %s", d.name, severity, d.description))
	}

	header := fmt.Sprintf(
		"[trajectory-health] Composite health score %d/10 - multiple trajectory "+
			"dimensions are degrading simultaneously. While no individual issue may "+
			"be severe enough to trigger its own alert, their accumulation signals an "+
			"unhealthy trajectory that is likely to result in wasted iterations or "+
			"failed completion. Consider: (1) stepping back to reassess your overall "+
			"approach, (2) reducing edit volume by planning before acting, "+
			"(3) verifying tool calls succeed before building on them, "+
			"(4) converting exploration into concrete action.\n",
		total,
	)
	return header + "Degraded dimensions:\n" + strings.Join(lines, "\n")
}
