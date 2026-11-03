package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	compoundingWindow = 10

	compoundingFailThreshold = 0.70

	compoundingMinCategories = 2
)

func toolCategory(toolName string) string {
	switch {
	case toolName == "edit_file" || toolName == "multi_edit_file" || toolName == "multi_file_edit" || toolName == "write_file" || toolName == "notebook_edit":
		return "editing"
	case toolName == "run_command" || toolName == "start_command":
		return "command"
	case toolName == "grep" || toolName == "search_files" || toolName == "code_search" || toolName == "glob":
		return "search"
	case toolName == "read_file" || toolName == "multi_file_read" || toolName == "list_directory":
		return "reading"
	case strings.HasPrefix(toolName, "lsp_"):
		return "lsp"
	case strings.HasPrefix(toolName, "git_"):
		return "git"
	default:
		return "other"
	}
}

type compoundingFailureState struct {
	window []compoundingEntry

	failedCategories map[string]bool

	fired bool
}

type compoundingEntry struct {
	toolName string
	isError  bool
	category string
}

func newCompoundingFailureState() *compoundingFailureState {
	return &compoundingFailureState{
		failedCategories: make(map[string]bool),
	}
}

func (c *compoundingFailureState) reset() {
	c.window = c.window[:0]
	c.failedCategories = make(map[string]bool)
	c.fired = false
}

func (c *compoundingFailureState) recordResult(toolName string, isError bool) {
	entry := compoundingEntry{
		toolName: toolName,
		isError:  isError,
		category: toolCategory(toolName),
	}

	if len(c.window) >= compoundingWindow {
		old := c.window[0]
		c.window = c.window[1:]

		if old.isError {
			stillPresent := false
			for _, e := range c.window {
				if e.isError && e.category == old.category {
					stillPresent = true
					break
				}
			}
			if !stillPresent {
				delete(c.failedCategories, old.category)
			}
		}
	}

	c.window = append(c.window, entry)
	if isError {
		c.failedCategories[entry.category] = true
	}
}

func (c *compoundingFailureState) check() string {
	if c.fired {
		return ""
	}

	if len(c.window) < compoundingWindow {
		return ""
	}

	failCount := 0
	for _, e := range c.window {
		if e.isError {
			failCount++
		}
	}

	failRate := float64(failCount) / float64(len(c.window))
	if failRate < compoundingFailThreshold {
		return ""
	}

	categoryCount := len(c.failedCategories)
	if categoryCount < compoundingMinCategories {
		return ""
	}

	c.fired = true

	cats := make([]string, 0, categoryCount)
	for cat := range c.failedCategories {
		cats = append(cats, cat)
	}

	debug.Log("compounding-failure",
		"sliding-window failure rate %.0f%% (%d/%d) across %d categories [%s] - strategy reset",
		failRate*100, failCount, len(c.window), categoryCount, strings.Join(cats, ", "))

	return fmt.Sprintf(
		"[strategy-reset] %d/%d tool calls failed (%.0f%%) across %d areas: %s. Change approach: re-read files, find root cause.",
		failCount, len(c.window), failRate*100, categoryCount, strings.Join(cats, ", "),
	)
}
