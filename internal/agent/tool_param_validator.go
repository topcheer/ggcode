package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Tool Parameter Pre-Validation
//
// Research basis: Tool-waste reduction in agentic systems (SICA arXiv:2504.15228)
// shows that 12-18% of failed tool calls are due to obviously invalid parameters
// (non-existent files, empty required fields, malformed paths) that could be
// caught BEFORE execution.
//
// This validator runs AFTER the LLM emits a tool call but BEFORE actual execution.
// It performs lightweight, deterministic checks (no LLM calls) to catch
// clear parameter errors and provide immediate feedback to the agent.
//
// Distinct from existing systems:
//   - Detectors (action_annihilate, tool_sequence): run AFTER execution, learn
//     from failures. This runs BEFORE, prevents failures.
//   - Required param validation: checks presence only. This checks VALUE validity.
//   - Error handling: catches runtime errors. This catches PRE-execution errors.

type paramValidator struct {
	maxHints int
	hints    int
}

func newParamValidator() *paramValidator {
	return &paramValidator{
		maxHints: 2, // at most 2 hints per run to avoid nagging
		hints:    0,
	}
}

func (v *paramValidator) reset() {
	v.hints = 0
}

// validateToolCall checks tool parameters for obvious errors before execution.
// Returns a non-empty guidance string if validation fails.
func (v *paramValidator) validateToolCall(toolName string, args map[string]interface{}) string {
	if v.hints >= v.maxHints {
		return ""
	}

	var issues []string

	// File-based tools: check path validity
	if fileArg, ok := getFileArg(toolName, args); ok {
		if issue := validateFilePath(fileArg); issue != "" {
			issues = append(issues, issue)
		}
	}

	// Command-based tools: check for dangerous or empty commands
	if cmdArg, ok := getCommandArg(toolName, args); ok {
		if issue := validateCommand(cmdArg); issue != "" {
			issues = append(issues, issue)
		}
	}

	// General: check for empty required fields
	if issue := validateRequiredFields(toolName, args); issue != "" {
		issues = append(issues, issue)
	}

	if len(issues) > 0 {
		v.hints++
		var b strings.Builder
		_, _ = fmt.Fprintf(&b, "[Parameter Validation] Tool call '%s' has issues:\n", toolName)
		for _, issue := range issues {
			_, _ = fmt.Fprintf(&b, "  - %s\n", issue)
		}
		_, _ = b.WriteString("Please correct the parameters and retry.\n")
		return b.String()
	}

	return ""
}

// getFileArg extracts the primary file path argument for file-based tools.
// Returns (path, true) if the tool uses a file path, ("", false) otherwise.
func getFileArg(toolName string, args map[string]interface{}) (string, bool) {
	fileTools := map[string]string{
		"read_file":       "path",
		"edit_file":       "file_path",
		"write_file":      "path",
		"multi_file_read": "files",
		"grep":            "path",
		"search_files":    "directory",
		"glob":            "pattern",
		"open_editor":     "path",
	}

	argName, isFileTool := fileTools[toolName]
	if !isFileTool {
		return "", false
	}

	val, ok := args[argName]
	if !ok {
		return "", false
	}

	// Handle both string and array paths
	switch v := val.(type) {
	case string:
		return v, true
	case []interface{}:
		if len(v) > 0 {
			if first, ok := v[0].(string); ok {
				return first, true
			}
		}
	case []map[string]interface{}:
		// For multi_file_read files array
		if len(v) > 0 {
			if path, ok := v[0]["path"].(string); ok {
				return path, true
			}
		}
	}

	return "", false
}

// getCommandArg extracts the command argument for command-based tools.
func getCommandArg(toolName string, args map[string]interface{}) (string, bool) {
	if toolName != "run_command" && toolName != "start_command" && toolName != "bash" {
		return "", false
	}

	val, ok := args["command"]
	if !ok {
		return "", false
	}

	if cmd, ok := val.(string); ok {
		return cmd, true
	}

	return "", false
}

// validateFilePath checks for obvious file path issues.
func validateFilePath(path string) string {
	if path == "" {
		return "file path is empty"
	}

	// Check for suspicious absolute paths outside workspace
	if filepath.IsAbs(path) && !strings.HasPrefix(path, "/Volumes/new/ggai") && !strings.HasPrefix(path, "/Users/") {
		return fmt.Sprintf("absolute path '%s' may be outside workspace", path)
	}

	// Check for path traversal attempts
	if strings.Contains(path, "../") {
		return "path contains '../' which may indicate unintended traversal"
	}

	// Check for obviously malformed paths
	if strings.ContainsAny(path, "\x00\r\n") {
		return "path contains invalid characters"
	}

	return ""
}

// validateCommand checks for dangerous or empty command patterns.
func validateCommand(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return "command is empty"
	}

	// Check for extremely dangerous commands (rm -rf /, format, etc.)
	lower := strings.ToLower(trimmed)
	dangerous := []string{
		"rm -rf /",
		"rm -rf /*",
		":(){:|:&};:", // fork bomb
		"> /dev/sda",  // disk overwrite
		":> /dev/sda",
		"format c:",
		"mkfs.",
	}

	for _, pattern := range dangerous {
		if strings.Contains(lower, pattern) {
			return fmt.Sprintf("command contains potentially destructive pattern: '%s'", pattern)
		}
	}

	return ""
}

// validateRequiredFields checks for empty or invalid required field values.
func validateRequiredFields(toolName string, args map[string]interface{}) string {
	// Tools with critical non-empty required fields
	criticalFields := map[string][]string{
		"edit_file":       {"file_path", "old_text", "new_text"},
		"write_file":      {"path", "content"},
		"multi_edit_file": {"file_path", "edits"},
		"git_commit":      {"message"},
		"web_fetch":       {"url"},
		"web_search":      {"query"},
		"lsp_definition":  {"path", "line", "character"},
		"lsp_references":  {"path", "line", "character"},
		"lsp_hover":       {"path", "line", "character"},
	}

	fields, hasCritical := criticalFields[toolName]
	if !hasCritical {
		return ""
	}

	var missing []string
	for _, field := range fields {
		val, ok := args[field]
		if !ok || isEmptyValue(val) {
			missing = append(missing, field)
		}
	}

	if len(missing) > 0 {
		return fmt.Sprintf("required field(s) missing or empty: %s", strings.Join(missing, ", "))
	}

	return ""
}

// isEmptyValue returns true if a value is empty/nil/zero-length.
func isEmptyValue(v interface{}) bool {
	if v == nil {
		return true
	}

	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val) == ""
	case []interface{}:
		return len(val) == 0
	case map[string]interface{}:
		return len(val) == 0
	case json.RawMessage:
		return len(val) == 0 || string(val) == "null"
	default:
		return false
	}
}
