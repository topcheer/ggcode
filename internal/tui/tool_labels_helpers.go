package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/util"
)

func askUserToolTarget(args map[string]any) string {
	if title := strings.TrimSpace(argString(args, "title")); title != "" {
		return title
	}
	rawQuestions, ok := args["questions"]
	if !ok {
		return ""
	}
	questions, ok := rawQuestions.([]any)
	if !ok || len(questions) == 0 {
		return ""
	}
	first, ok := questions[0].(map[string]any)
	if !ok {
		return ""
	}
	title := strings.TrimSpace(util.FirstNonEmpty(
		argAnyString(first["title"]),
		argAnyString(first["prompt"]),
	))
	if title == "" {
		return ""
	}
	if len(questions) == 1 {
		return title
	}
	return fmt.Sprintf("%s +%d", title, len(questions)-1)
}

func argAnyString(v any) string {
	s, _ := v.(string)
	return s
}

func toolPresentationFor(lang Language, action, target string) toolPresentation {
	return toolPresentation{
		DisplayName: localizedToolLabel(lang, action),
		Detail:      target,
		Activity:    localizedToolActivity(lang, action, target),
	}
}

func toolLabelFor(lang Language, action string) string {
	return localizedToolLabel(lang, action)
}

func localizedGenericActivity(lang Language, label string) string {
	if lang == LangZhCN {
		return "运行 " + label
	}
	return "Running " + label
}

func localizedCommandActivity(lang Language, title string) string {
	if lang == LangZhCN {
		return "执行 " + title
	}
	return "Running " + title
}

func localizedGenericToolName(lang Language, name string) string {
	if lang == LangZhCN {
		return strings.ReplaceAll(name, "_", " ")
	}
	return prettifyToolName(name)
}

func formatToolInline(name, detail string) string {
	if isTrivialToolDetail(detail) {
		return name
	}
	return name + " " + detail
}

func parseToolArgs(raw string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

func compactToolArgsPreview(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if isTrivialToolDetail(trimmed) {
		return ""
	}
	args := parseToolArgs(raw)
	if args == nil {
		return compactSingleLine(raw)
	}
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"file_path", "path", "directory", "file", "filename"} {
		if value, ok := args[key].(string); ok {
			args[key] = displayToolFileTarget(value)
		}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return compactSingleLine(raw)
	}
	return compactSingleLine(string(b))
}

func isTrivialToolDetail(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "{}", "[]", "null":
		return true
	default:
		return false
	}
}

func argString(args map[string]any, key string) string {
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
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// parseStringSlice extracts a string array from parsed args.
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

func rawArgString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

func rawCommandArg(args map[string]any) string {
	return util.FirstNonEmpty(
		rawArgString(args, "command"),
		rawArgString(args, "cmd"),
	)
}

func shortenJobID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// lspToolPresentation creates a presentation for LSP tools showing file:line.
func lspToolPresentation(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	// Map LSP tool names to short action labels
	var action string
	switch toolName {
	case "lsp_hover":
		action = "hover"
	case "lsp_definition":
		action = "definition"
	case "lsp_references":
		action = "references"
	case "lsp_symbols":
		action = "symbols"
	case "lsp_workspace_symbols":
		action = "workspace symbols"
	case "lsp_diagnostics":
		action = "diagnostics"
	case "lsp_code_actions":
		action = "code actions"
	case "lsp_rename":
		action = "rename"
	case "lsp_implementation":
		action = "implementation"
	case "lsp_prepare_call_hierarchy":
		action = "call hierarchy"
	case "lsp_incoming_calls":
		action = "incoming calls"
	case "lsp_outgoing_calls":
		action = "outgoing calls"
	default:
		action = strings.TrimPrefix(toolName, "lsp_")
	}

	// Build detail: "file:line" or just "file"
	detail := fileTarget
	if detail == "" {
		detail = displayToolFileTarget(argString(args, "path"))
	}
	line := argString(args, "line")
	if line != "" && detail != "" {
		detail = detail + ":" + line
	}

	// For rename, include the new name
	if toolName == "lsp_rename" {
		newName := argString(args, "new_name")
		if newName != "" {
			detail = fmt.Sprintf("%s → %s", detail, newName)
		}
	}

	displayName := "LSP"
	switch lang {
	case LangZhCN:
		activity := "LSP " + action
		if detail != "" {
			activity = "LSP " + action + " " + detail
		}
		return toolPresentation{
			DisplayName: displayName,
			Detail:      detail,
			Activity:    activity,
		}
	default:
		activity := "LSP " + action
		if detail != "" {
			activity = "LSP " + action + " " + detail
		}
		return toolPresentation{
			DisplayName: displayName,
			Detail:      detail,
			Activity:    activity,
		}
	}
}

func displayToolTarget(value string) string {
	value = strings.TrimSpace(value)
	value = compactSingleLine(value)
	cwd, _ := os.Getwd()
	return util.FormatToolDetail(value, cwd)
}

func displayToolFileTarget(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimRight(value, `/\`)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "./")
	// Try to make absolute paths relative to cwd
	if filepath.IsAbs(value) {
		cwd, _ := os.Getwd()
		normCWD := normalizeDisplayPath(cwd)
		normValue := normalizeDisplayPath(value)
		if rel, relErr := filepath.Rel(normCWD, normValue); relErr == nil && !strings.HasPrefix(rel, "..") {
			value = filepath.ToSlash(rel)
		}
	}
	cwd, _ := os.Getwd()
	return util.FormatToolDetail(value, cwd)
}

func normalizeDisplayPath(value string) string {
	value = filepath.Clean(value)
	if resolved, err := filepath.EvalSymlinks(value); err == nil {
		return resolved
	}
	dir := filepath.Dir(value)
	base := filepath.Base(value)
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, base)
	}
	return value
}

// firstLine returns the first line of a multi-line string.
func firstLine(s string) string {
	if idx := strings.Index(s, "\n"); idx >= 0 {
		return s[:idx]
	}
	return s
}

// friendlyToolName returns a short, user-friendly name for a tool,
// with special mappings for common tools and prettifyToolName as fallback.
func friendlyToolName(name string) string {
	switch name {
	case "run_command":
		return "Bash"
	case "start_command":
		return "Background Bash"
	default:
		return prettifyToolName(name)
	}
}

func prettifyToolName(name string) string {
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	parts := strings.Fields(name)
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}

// relativizeResult replaces absolute paths in tool result text with relative paths.
func relativizeResult(text string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return text
	}
	return util.RelativizePaths(text, cwd)
}
