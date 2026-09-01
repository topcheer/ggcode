package extpane

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ANSI color/style codes.
const (
	cReset   = "\x1b[0m"
	cBold    = "\x1b[1m"
	cDim     = "\x1b[2m"
	cRed     = "\x1b[31m"
	cGreen   = "\x1b[32m"
	cYellow  = "\x1b[33m"
	cBlue    = "\x1b[34m"
	cMagenta = "\x1b[35m"
	cCyan    = "\x1b[36m"
	cGray    = "\x1b[90m"

	cBrightBlue  = "\x1b[94m"
	cBrightGreen = "\x1b[92m"
)

// formatHeader creates a visually distinct header for the start of an agent's output.
func formatHeader(name, kind string) string {
	icon := "◆"
	label := "Sub-Agent"
	accent := cBrightBlue
	if kind == "teammate" {
		icon = "●"
		label = "Teammate"
		accent = cMagenta
	}
	rule := strings.Repeat("─", 52)
	return fmt.Sprintf("%s%s %s %s%s\n%s%s\n%s\n\n",
		accent+cBold, icon, label, name, cReset,
		cGray, rule, cReset)
}

// formatToolCall renders a tool call as a clean two-line block.
// No fixed-width boxes — adapts to any terminal width.
//
//	▸ read_file
//	  internal/tui/extpane/manager.go
func formatToolCall(toolName, detail string) string {
	detail = compactPreview(detail)
	header := fmt.Sprintf("%s▸ %s%s\n", cCyan+cBold, toolName, cReset)
	if detail != "" {
		header += fmt.Sprintf("%s  %s%s\n", cDim, detail, cReset)
	}
	return header + "\n"
}

// formatToolResult renders a tool result with status icon.
//
//	✓ read_file  245 lines · 12.3 KB
func formatToolResult(toolName, result string, isError bool) string {
	result = compactPreview(result)
	if isError {
		return fmt.Sprintf("%s✗ %s%s  %s%s%s\n\n",
			cRed+cBold, toolName, cReset,
			cRed, result, cReset)
	}
	return fmt.Sprintf("%s✓ %s%s  %s%s%s\n\n",
		cBrightGreen, toolName, cReset,
		cDim, result, cReset)
}

// formatDone renders a completion banner.
func formatDone(isError bool) string {
	ts := time.Now().Format("15:04:05")
	if isError {
		return fmt.Sprintf("\n%s━━ ✗ FAILED ━━━━━━━━━━━━━━━━━━━%s\n\n", cRed+cBold, cReset)
	}
	return fmt.Sprintf("\n%s━━ ✓ done · %s ━━━━━━━━━━━━━━━━━%s\n\n",
		cGray, ts, cReset)
}

// stripAnsi removes ANSI escape sequences (CSI and two-byte ESC variants).
// Local copy: extpane cannot import the tui package's stripAnsiForChat
// (import cycle). Used BEFORE truncation so a boundary cut can never leave
// a partial CSI that swallows following text (#1368).
func stripAnsi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	const (
		statePlain  = iota
		stateEsc    // saw ESC, deciding CSI vs two-byte
		stateCsi    // inside CSI [... final @-~
		stateOsc    // #1414-B: inside OSC ] ... BEL/ST
		stateOscEsc // OSC saw ESC: ST (\) ends, else stay
	)
	st := statePlain
	for _, c := range s {
		switch st {
		case statePlain:
			if c == '\x1b' {
				st = stateEsc
			} else {
				b.WriteRune(c)
			}
		case stateEsc:
			if c == '[' {
				st = stateCsi
			} else if c == ']' {
				// OSC: ESC ] ... terminated by BEL or ST (ESC \).
				// #1414-B: the old code fell through to the two-byte
				// branch here - ']' was emitted and the OSC BODY (titles,
				// hyperlink URLs) plus BEL leaked verbatim into the
				// preview pane as invisible control bytes.
				st = stateOsc
			} else {
				st = statePlain // two-byte ESC sequence (e.g. \x1bM)
				b.WriteRune(c)
			}
		case stateCsi:
			if c >= 0x40 && c <= 0x7E {
				st = statePlain
			}
		case stateOsc:
			if c == '\x07' { // BEL terminator
				st = statePlain
			} else if c == '\x1b' {
				st = stateOscEsc
			}
		case stateOscEsc:
			if c == '\\' { // ST terminator
				st = statePlain
			} else {
				st = stateOsc // not ST; keep consuming OSC body
			}
		}
	}
	return b.String()
}

// compactPreview trims and truncates a string for inline display.
// UTF-8 aware: never cuts in the middle of a rune.
//
// #1368: order matters - ANSI strip and newline replacement run BEFORE the
// 100-byte truncation. The old order truncated first, so (a) each "\n"
// expanded to " ↵ " (+4 bytes) AFTER the size check, blowing the cap ~5x on
// blank-line-heavy output, and (b) a CSI sequence cut at the boundary left
// a partial escape whose following bytes the terminal ate as parameters.
func compactPreview(s string) string {
	s = stripAnsi(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ↵ ")
	s = strings.TrimSpace(s)
	if len(s) > 100 {
		cut := 97
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}
