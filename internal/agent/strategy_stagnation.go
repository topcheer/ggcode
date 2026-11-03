package agent

import (
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/util"
)

const (
	stagnationHistorySize = 8

	stagnationFailureThreshold = 3

	stagnationMaxWarnings = 2

	shellHealthProbeMarker = util.ShellHealthProbeCommand
)

func isShellOutageFailure(errContent string) bool {
	if errContent == "" {
		return false
	}
	c := strings.ToLower(errContent)
	return strings.Contains(c, "fork/exec") && strings.Contains(c, "no such file or directory")
}

type stagnationAttempt struct {
	toolName string
	target   string
	success  bool
}

type strategyStagnationState struct {
	recent   []stagnationAttempt
	warnings int
}

func newStrategyStagnationState() *strategyStagnationState {
	return &strategyStagnationState{}
}

func (s *strategyStagnationState) reset() {
	s.recent = nil
	s.warnings = 0
}

func stagnationToolCovered(toolName string) bool {
	switch toolName {
	case "edit_file", "write_file", "read_file", "multi_edit_file",
		"lsp_diagnostics", "lsp_symbols", "lsp_definition",
		"run_command", "start_command",
		"grep", "search_files",
		"git_add", "git_commit", "git_diff", "git_status":
		return true
	}
	return false
}

func extractStagnationTarget(toolName, argsJSON string) string {
	switch toolName {
	case "edit_file", "write_file", "read_file", "multi_edit_file",
		"lsp_diagnostics", "lsp_symbols", "lsp_definition":
		return extractJSONStringFieldStag(argsJSON, "file_path", "path")
	case "run_command", "start_command":
		return extractJSONStringFieldStag(argsJSON, "command")
	case "grep", "search_files":
		p := extractJSONStringFieldStag(argsJSON, "pattern")
		if p != "" {
			return p
		}
		return extractJSONStringFieldStag(argsJSON, "query")
	case "git_add", "git_commit", "git_diff", "git_status":
		return extractJSONStringFieldStag(argsJSON, "path")
	default:
		return ""
	}
}

func extractJSONStringFieldStag(jsonStr string, keys ...string) string {
	for _, k := range keys {

		search := "\"" + k + "\":\""
		idx := strings.Index(jsonStr, search)
		if idx == -1 {

			search = "\"" + k + "\": \""
			idx = strings.Index(jsonStr, search)
			if idx == -1 {
				continue
			}
		}
		start := idx + len(search)
		end := strings.Index(jsonStr[start:], "\"")
		if end == -1 {
			continue
		}
		return jsonStr[start : start+end]
	}
	return ""
}

func (s *strategyStagnationState) recordAttempt(toolName, argsJSON string, success bool) bool {
	return s.recordAttemptDetail(toolName, argsJSON, success, "")
}

func isShellHealthProbe(argsJSON string) bool {
	return strings.Contains(argsJSON, shellHealthProbeMarker)
}

func (s *strategyStagnationState) recordAttemptDetail(toolName, argsJSON string, success bool, errContent string) bool {
	target := extractStagnationTarget(toolName, argsJSON)

	if !stagnationToolCovered(toolName) {
		return false
	}
	attempt := stagnationAttempt{
		toolName: toolName,
		target:   target,
		success:  success,
	}

	if !success && toolName == "run_command" && isShellHealthProbe(argsJSON) {
		return false
	}

	if !success && toolName == "run_command" && isShellOutageFailure(errContent) {
		return false
	}

	s.recent = append(s.recent, attempt)
	if len(s.recent) > stagnationHistorySize {
		s.recent = s.recent[1:]
	}

	if success || len(s.recent) < stagnationFailureThreshold {
		return false
	}

	consecutiveFailures := 0
	for i := len(s.recent) - 1; i >= 0; i-- {
		entry := s.recent[i]
		if entry.toolName == toolName && entry.target == target && !entry.success {
			consecutiveFailures++
		} else {
			break
		}
	}

	if consecutiveFailures >= stagnationFailureThreshold && s.warnings < stagnationMaxWarnings {
		s.warnings++
		return true
	}

	return false
}

func strategyStagnationWarning(toolName, target string, failures int) string {
	msg := "[STRATEGY-STAGNATION] " + toolName
	if target != "" {
		t := target
		if len(t) > 60 {
			t = t[:60] + "..."
		}
		msg += " on " + t
	}
	msg += " has failed " + strconv.Itoa(failures) + " consecutive times with the same approach. " +
		"You are stuck in a local minimum. STOP retrying with the same strategy. " +
		"Pivot to a fundamentally different approach: " +
		"(1) re-read the file/error to understand the root cause, " +
		"(2) try a different tool or method, " +
		"(3) simplify the operation into smaller steps, " +
		"(4) verify assumptions about the current state. " +
		"Repeating the same action expecting different results is a waste of tokens."
	return msg
}
