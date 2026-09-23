package tui

import (
	"strings"

	"github.com/topcheer/ggcode/internal/util"
)

// describeFileFamilyTool renders file/search/list tools.
func describeFileFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
	case "read_file":
		return toolPresentationFor(lang, "read", fileTarget)
	case "edit_file":
		if strings.TrimSpace(argString(args, "old_text")) == "" && fileTarget != "" {
			return toolPresentationFor(lang, "create", fileTarget)
		}
		return toolPresentationFor(lang, "edit", fileTarget)
	case "write_file":
		return toolPresentationFor(lang, "write", fileTarget)
	case "glob":
		pattern := displayToolTarget(argString(args, "pattern"))
		return toolPresentationFor(lang, "find", pattern)
	case "grep", "search_files":
		searchTarget := displayToolTarget(util.FirstNonEmpty(
			argString(args, "pattern"),
			argString(args, "query"),
			argString(args, "path"),
		))
		return toolPresentationFor(lang, "search", searchTarget)
	case "list_directory":
		listTarget := displayToolFileTarget(util.FirstNonEmpty(
			argString(args, "path"),
			argString(args, "directory"),
		))
		return toolPresentationFor(lang, "list", listTarget)
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
