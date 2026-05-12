package tui

import (
	"fmt"
	"strings"
)

func commandToolPresentation(lang Language, rawCommand string) (toolPresentation, bool) {
	rawCommand = relativizeResult(rawCommand)
	preview := buildCommandPreview(rawCommand)
	if preview.Title == "" {
		return toolPresentation{}, false
	}
	title := displayToolTarget(preview.Title)
	// Build detail from command lines (showing script preview alongside the title)
	var detailParts []string
	for _, line := range preview.CommandLines {
		part := compactSingleLine(strings.TrimRight(line, " \t"))
		detailParts = append(detailParts, part)
	}
	// Show at most 2 command lines in detail to avoid header bloat
	maxDetailLines := 2
	if len(detailParts) > maxDetailLines {
		hidden := len(detailParts) - maxDetailLines + preview.CommandHiddenLineCount
		detailParts = detailParts[:maxDetailLines]
		detailParts = append(detailParts, fmt.Sprintf("+%d more", hidden))
	} else if preview.CommandHiddenLineCount > 0 {
		detailParts = append(detailParts, fmt.Sprintf("+%d more", preview.CommandHiddenLineCount))
	}
	detail := strings.Join(detailParts, "; ")
	return toolPresentation{
		DisplayName: title,
		Detail:      detail,
		Activity:    localizedCommandActivity(lang, title),
	}, true
}

func buildCommandPreview(rawCommand string) commandPreview {
	lines := commandPreviewLines(rawCommand)
	if len(lines) == 0 {
		return commandPreview{}
	}

	titleIndex, title := leadingCommentTitle(lines)
	previewLines := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == titleIndex {
			continue
		}
		previewLines = append(previewLines, compactSingleLine(strings.TrimRight(line, " \t")))
	}

	return commandPreview{
		Title:                  title,
		CommandLines:           previewLines,
		CommandHiddenLineCount: hiddenPreviewLineCount(len(previewLines)),
	}
}

func hiddenPreviewLineCount(total int) int {
	if total <= maxPreviewLines {
		return 0
	}
	return total - maxPreviewLines
}

func commandPreviewLines(rawCommand string) []string {
	rawCommand = strings.ReplaceAll(rawCommand, "\r\n", "\n")
	rawCommand = strings.TrimSpace(rawCommand)
	if rawCommand == "" {
		return nil
	}

	lines := strings.Split(rawCommand, "\n")
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func leadingCommentTitle(lines []string) (int, string) {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "#") {
			return -1, ""
		}
		title := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
		if title != "" {
			return i, title
		}
	}
	return -1, ""
}
