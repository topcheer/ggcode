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
	// #1014: defense in depth - expand control characters that slipped past
	// upstream sanitizers into spaces BEFORE measuring; lipgloss counts them
	// as width 0 while the terminal expands TABs to tab stops, so a padded
	// line could still land past the border column.
	//
	// R201 fix: the expansion must be ANSI-AWARE. The panel renders its rows
	// through lipgloss FIRST (e.g. summaries in Foreground(Color("8")) =
	// SGR 90, the cursor row Bold+Color("12") = SGR 1;94), so styled lines
	// legitimately contain ESC sequences. The old blanket r<0x20 expansion
	// replaced the ESC byte of those sequences with a space, leaving the
	// sequence body visible as literal text - every row showed "[90m ... [m"
	// in the /sessions panel. Escape sequences are zero-width for
	// lipgloss.Width and are copied through verbatim; every OTHER control
	// character (TAB/CR/0x7f, the actual #1014 concern) still expands.
	if strings.ContainsFunc(line, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		line = expandControlCharsANSIAware(line)
	}
	visible := lipgloss.Width(line)
	if visible >= width {
		return line
	}
	return line + strings.Repeat(" ", width-visible)
}

// expandControlCharsANSIAware replaces control characters with spaces while
// copying ANSI escape sequences through untouched. A sequence started by ESC
// is consumed as: CSI (ESC [ ... final byte @-~), OSC/DCS/APC/PM (ESC ] / P /
// X / ^ / _ ... terminated by BEL or ESC \), or a two-byte escape (ESC <char>).
// A lone ESC at end-of-line expands to a space like any other control char.
func expandControlCharsANSIAware(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	i := 0
	for i < len(line) {
		c := line[i]
		if c != 0x1b {
			if c < 0x20 || c == 0x7f {
				b.WriteByte(' ')
			} else {
				b.WriteByte(c)
			}
			i++
			continue
		}
		if i+1 >= len(line) {
			b.WriteByte(' ') // lone trailing ESC
			break
		}
		switch line[i+1] {
		case '[': // CSI: copy through the final byte (@-~)
			j := i + 2
			for j < len(line) {
				if line[j] >= 0x40 && line[j] <= 0x7e {
					break
				}
				j++
			}
			if j < len(line) {
				j++ // include the final byte
			}
			b.WriteString(line[i:j])
			i = j
		case ']', 'P', 'X', '^', '_': // string sequences: BEL or ESC \ terminate
			j := i + 2
			end := -1
			for j < len(line) {
				if line[j] == 0x07 {
					end = j + 1
					break
				}
				if line[j] == 0x1b && j+1 < len(line) && line[j+1] == '\\' {
					end = j + 2
					break
				}
				j++
			}
			if end < 0 {
				end = len(line)
			}
			b.WriteString(line[i:end])
			i = end
		default: // two-byte escape (ESC 7, ESC (, ...)
			b.WriteString(line[i : i+2])
			i += 2
		}
	}
	return b.String()
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
