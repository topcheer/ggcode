package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/muesli/reflow/wordwrap"
)

func joinPanelColumns(leftLines, rightLines []string, leftWidth, rightWidth, height int) string {
	actualHeight := height
	if len(leftLines) > actualHeight {
		actualHeight = len(leftLines)
	}
	if len(rightLines) > actualHeight {
		actualHeight = len(rightLines)
	}
	leftLines = normalizePanelLines(leftLines, actualHeight)
	rightLines = normalizePanelLines(rightLines, actualHeight)
	rows := make([]string, 0, actualHeight)
	for i := 0; i < actualHeight; i++ {
		rows = append(rows, padPanelLine(leftLines[i], leftWidth)+"  "+padPanelLine(rightLines[i], rightWidth))
	}
	return strings.Join(rows, "\n")
}

func wrapPanelText(content string, width, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	if width <= 0 {
		width = 1
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{""}
	}
	var lines []string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, " ")
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		wrapped := wordwrap.String(line, width)
		for _, candidate := range strings.Split(wrapped, "\n") {
			lines = append(lines, hardWrapPanelLine(candidate, width)...)
		}
		if len(lines) >= maxLines {
			return lines[:maxLines]
		}
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}

func normalizePanelLines(lines []string, height int) []string {
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func padPanelLine(line string, width int) string {
	visible := lipgloss.Width(line)
	if visible >= width {
		return line
	}
	return line + strings.Repeat(" ", width-visible)
}

func hardWrapPanelLine(line string, width int) []string {
	line = strings.TrimRight(line, " ")
	if line == "" {
		return []string{""}
	}
	if width <= 0 {
		return []string{line}
	}
	var out []string
	remaining := line
	for remaining != "" {
		if lipgloss.Width(remaining) <= width {
			out = append(out, remaining)
			break
		}
		cut := 0
		currentWidth := 0
		for i, r := range remaining {
			rw := lipgloss.Width(string(r))
			if currentWidth+rw > width {
				break
			}
			currentWidth += rw
			cut = i + len(string(r))
		}
		if cut <= 0 {
			break
		}
		out = append(out, strings.TrimRight(remaining[:cut], " "))
		remaining = strings.TrimLeft(remaining[cut:], " ")
	}
	if len(out) == 0 {
		return []string{line}
	}
	return out
}
