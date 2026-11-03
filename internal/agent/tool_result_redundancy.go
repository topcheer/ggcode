package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	trMinLines = 5

	trOverlapThreshold = 0.6

	trMaxWarnings = 2

	trWindowSize = 8

	trMaxLineLen = 200
)

var trStatePollingTools = map[string]bool{
	"ci_status":           true,
	"git_status":          true,
	"git_log":             true,
	"git_diff":            true,
	"read_command_output": true,
	"wait_command":        true,
	"list_commands":       true,
	"task_list":           true,
	"task_output":         true,
	"list_agents":         true,
	"wait_agent":          true,
	"teammate_list":       true,
	"teammate_results":    true,
	"swarm_task_list":     true,
	"debug_log":           true,
	"lsp_diagnostics":     true,
	"read_file":           false,
	"lanchat":             true,
}

type toolResultEntry struct {
	toolName string
	lines    map[string]bool
	iter     int
}

type toolResultRedundancyState struct {
	entries        []toolResultEntry
	warningsFired  int
	lastWarnedIter int
}

func newToolResultRedundancyState() *toolResultRedundancyState {
	return &toolResultRedundancyState{}
}

func (t *toolResultRedundancyState) reset() {
	t.entries = nil
	t.warningsFired = 0
	t.lastWarnedIter = 0
}

func (t *toolResultRedundancyState) recordToolCall(toolName string, args json.RawMessage, content string, iteration int) string {
	if trIsVerifyRerun(toolName, args) {
		return ""
	}
	return t.recordResult(toolName, content, iteration)
}

func trIsVerifyRerun(toolName string, args json.RawMessage) bool {
	if toolName != "run_command" {
		return false
	}
	cmd := extractCommandFromArgs(args)
	return cmd != "" && isVerifyCommand(cmd)
}

func (t *toolResultRedundancyState) recordResult(toolName, content string, iteration int) string {

	if trStatePollingTools[toolName] {
		return ""
	}

	if t.warningsFired >= trMaxWarnings {
		t.storeEntry(toolName, content, iteration)
		return ""
	}

	lines := trNormalize(content)
	if len(lines) < trMinLines {
		t.storeEntry(toolName, content, iteration)
		return ""
	}

	for _, entry := range t.entries {
		if len(entry.lines) < trMinLines {
			continue
		}
		jaccard := trJaccard(lines, entry.lines)
		if jaccard >= trOverlapThreshold {

			if t.warningsFired > 0 && iteration <= t.lastWarnedIter+1 {
				t.storeEntry(toolName, content, iteration)
				return ""
			}
			t.warningsFired++
			t.lastWarnedIter = iteration
			t.storeEntry(toolName, content, iteration)

			debug.Log("tool_result_redundancy",
				"warning at iter %d: %s overlaps prior %s (Jaccard=%.2f)",
				iteration, toolName, entry.toolName, jaccard)

			return fmt.Sprintf(
				"[tool-result-redundancy] '%s' overlaps (%.0f%%) with prior '%s' call. Use existing context instead of re-fetching.",
				toolName, jaccard*100, entry.toolName,
			)
		}
	}

	t.storeEntry(toolName, content, iteration)
	return ""
}

func (t *toolResultRedundancyState) storeEntry(toolName, content string, iteration int) {
	lines := trNormalize(content)
	if len(lines) < trMinLines {
		return
	}
	t.entries = append(t.entries, toolResultEntry{
		toolName: toolName,
		lines:    lines,
		iter:     iteration,
	})
	if len(t.entries) > trWindowSize {
		t.entries = t.entries[1:]
	}
}

func trNormalize(content string) map[string]bool {
	lines := make(map[string]bool)
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if len(line) < 4 {
			continue
		}
		if len(line) > trMaxLineLen {
			line = line[:trMaxLineLen]
		}
		lines[line] = true
	}
	return lines
}

func trJaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
