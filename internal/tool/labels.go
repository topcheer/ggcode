package tool

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ToolPresentation holds the human-readable display info for a tool call.
type ToolPresentation struct {
	DisplayName string // e.g. "Read", "Edit", "go test ./..."
	Detail      string // e.g. "/tmp/test.go", ""
}

// DescribeTool returns a human-readable presentation for a tool call.
// It picks the key argument(s) for each tool and formats them compactly.
// This is the shared implementation used by TUI, daemon, IM, and ACP.
func DescribeTool(toolName, rawArgs string) ToolPresentation {
	args := parseToolArgs(rawArgs)
	fileTarget := extractFileTarget(toolName, rawArgs)

	switch toolName {
	case "read_file":
		return toolPres("Read", fileTarget)
	case "edit_file":
		if strings.TrimSpace(argStr(args, "old_text")) == "" && fileTarget != "" {
			return toolPres("Create", fileTarget)
		}
		return toolPres("Edit", fileTarget)
	case "write_file":
		return toolPres("Write", fileTarget)
	case "multi_edit":
		return toolPres("Edit", fileTarget)
	case "glob":
		return toolPres("Find", displayTarget(argStr(args, "pattern")))
	case "grep", "search_files":
		return toolPres("Search", displayTarget(firstNonEmpty(
			argStr(args, "pattern"),
			argStr(args, "query"),
			argStr(args, "path"),
		)))
	case "list_directory":
		return toolPres("List", displayFileTarget(firstNonEmpty(
			argStr(args, "path"),
			argStr(args, "directory"),
		)))
	case "run_command", "bash", "powershell", "start_command":
		cmd := firstNonEmpty(argStr(args, "command"), argStr(args, "cmd"))
		if cmd != "" {
			return toolPres(compactSingleLineNoTruncate(cmd), "")
		}
		return toolPres("Run", "")
	case "write_command_input":
		inputText := argStr(args, "input")
		jobID := argStr(args, "job_id")
		if inputText != "" {
			if len(inputText) > 60 {
				inputText = inputText[:57] + "…"
			}
			detail := "→ " + inputText
			if jobID != "" {
				detail = "[" + shortenID(jobID) + "] " + detail
			}
			return toolPres("Input", detail)
		}
		return toolPres("Input", firstNonEmpty(jobID, "background command"))
	case "read_command_output":
		return toolPres("Output", displayTarget(shortenID(argStr(args, "job_id"))))
	case "wait_command":
		jobID := argStr(args, "job_id")
		waitSec := argStr(args, "wait_seconds")
		detail := shortenID(jobID)
		if waitSec != "" {
			detail = fmt.Sprintf("%s (%ss)", detail, waitSec)
		}
		return toolPres("Wait", displayTarget(detail))
	case "stop_command":
		return toolPres("Stop", displayTarget(shortenID(argStr(args, "job_id"))))
	case "list_commands":
		return toolPres("Background Jobs", "")
	case "web_fetch":
		return toolPres("Fetch", displayTarget(argStr(args, "url")))
	case "web_search":
		return toolPres("Search", displayTarget(argStr(args, "query")))
	case "todo_write":
		return toolPres("Update Todos", "")
	case "ask_user":
		return toolPres("Ask", displayTarget(askUserTarget(args)))
	case "skill":
		return toolPres("Skill", displayTarget(argStr(args, "skill")))
	case "save_memory":
		return toolPres("Save Memory", argStr(args, "key"))
	case "git_status":
		return toolPres("Inspect", displayFileTarget(argStr(args, "path")))
	case "git_diff":
		detail := ""
		if argStr(args, "cached") == "true" {
			detail = "--cached"
		}
		if f := argStr(args, "file"); f != "" {
			if detail != "" {
				detail += " "
			}
			detail += displayFileTarget(f)
		}
		return toolPres("Diff", detail)
	case "git_log":
		return toolPres("Log", "")
	case "git_show":
		return toolPres("Show", displayTarget(argStr(args, "revision")))
	case "git_blame":
		return toolPres("Blame", displayFileTarget(argStr(args, "file")))
	case "git_branch_list":
		detail := ""
		if argStr(args, "remote") == "true" {
			detail = "--remote"
		}
		return toolPres("Branches", detail)
	case "git_remote":
		return toolPres("Remote", "")
	case "git_stash_list":
		return toolPres("Stash", "list")
	case "git_add":
		files := parseStringSlice(args, "files")
		return toolPres("Stage", displayFileTarget(strings.Join(files, ", ")))
	case "git_commit":
		return toolPres("Commit", compactSingleLine(argStr(args, "message")))
	case "git_stash":
		action := argStr(args, "action")
		if action == "" {
			action = "push"
		}
		return toolPres("Stash", action)
	case "sleep":
		sec, _ := strconv.Atoi(argStr(args, "seconds"))
		ms, _ := strconv.Atoi(argStr(args, "milliseconds"))
		d := time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
		return toolPres("Sleep", d.String())
	case "spawn_agent":
		return toolPres("Spawn Agent", displayTarget(firstNonEmpty(
			argStr(args, "description"),
			argStr(args, "prompt"),
		)))
	case "wait_agent":
		return toolPres("Wait Agent", displayTarget(argStr(args, "task_id")))
	case "list_agents":
		return toolPres("List Agents", "")
	default:
		// MCP tools or unknown — prettify the name
		return toolPres(prettifyToolName(toolName), compactArgsPreview(rawArgs))
	}
}

// FormatToolInline combines DisplayName and Detail into a single display string,
// matching the TUI format: "Read /tmp/test.go" or "go test ./...".
func FormatToolInline(name, detail string) string {
	if detail == "" {
		return name
	}
	return name + " " + detail
}

// prettifyToolName converts snake_case tool names to Title Case.
func prettifyToolName(name string) string {
	// Split on underscores and capitalize each part
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// --- internal helpers ---

func toolPres(name, detail string) ToolPresentation {
	return ToolPresentation{DisplayName: name, Detail: detail}
}

func parseToolArgs(raw string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

func argStr(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	switch vv := v.(type) {
	case string:
		return vv
	case float64:
		return strconv.FormatFloat(vv, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(vv)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func displayTarget(s string) string {
	return strings.TrimSpace(s)
}

func displayFileTarget(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.TrimRight(s, `/\`)
}

func extractFileTarget(toolName, rawArgs string) string {
	// Try known file-path fields first
	args := parseToolArgs(rawArgs)
	for _, key := range []string{"file_path", "path", "directory", "file", "filename"} {
		if v, ok := args[key].(string); ok && v != "" {
			return displayFileTarget(v)
		}
	}
	return ""
}

func compactArgsPreview(raw string) string {
	raw = strings.TrimSpace(raw)
	if isTrivialDetail(raw) {
		return ""
	}
	args := parseToolArgs(raw)
	if args == nil {
		return compactSingleLine(raw)
	}
	if len(args) == 0 {
		return ""
	}
	// Shorten file paths
	for _, key := range []string{"file_path", "path", "directory", "file", "filename"} {
		if v, ok := args[key].(string); ok {
			args[key] = displayFileTarget(v)
		}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return compactSingleLine(raw)
	}
	return compactSingleLine(string(b))
}

func compactSingleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// compactSingleLineNoTruncate compacts but never truncates (for command display).
func compactSingleLineNoTruncate(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

func isTrivialDetail(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

func shortenID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func parseStringSlice(args map[string]any, key string) []string {
	if args == nil {
		return nil
	}
	v, ok := args[key]
	if !ok {
		return nil
	}
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		var result []string
		for _, item := range vv {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func askUserTarget(args map[string]any) string {
	if args == nil {
		return ""
	}
	// Try to extract a short prompt summary
	if prompt, ok := args["prompt"].(string); ok && prompt != "" {
		if len(prompt) > 60 {
			return prompt[:57] + "…"
		}
		return prompt
	}
	if questions, ok := args["questions"].([]any); ok && len(questions) > 0 {
		if first, ok := questions[0].(map[string]any); ok {
			if title, ok := first["title"].(string); ok {
				return title
			}
			if prompt, ok := first["prompt"].(string); ok {
				if len(prompt) > 60 {
					return prompt[:57] + "…"
				}
				return prompt
			}
		}
	}
	return ""
}

// RelativizePath tries to make an absolute path relative to workingDir.
func RelativizePath(path, workingDir string) string {
	if workingDir == "" {
		return path
	}
	rel, err := filepath.Rel(workingDir, path)
	if err != nil {
		return path
	}
	if strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}
