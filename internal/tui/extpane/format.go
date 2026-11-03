package extpane

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	cReset   = "\x1b[0m"
	cBold    = "\x1b[1m"
	cDim     = "\x1b[2m"
	cRed     = "\x1b[31m"
	cMagenta = "\x1b[35m"
	cCyan    = "\x1b[36m"
	cGray    = "\x1b[90m"

	cBrightBlue  = "\x1b[94m"
	cBrightGreen = "\x1b[92m"
)

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

func formatToolCall(toolName, detail string) string {
	detail = compactPreview(detail)
	header := fmt.Sprintf("%s▸ %s%s\n", cCyan+cBold, toolName, cReset)
	if detail != "" {
		header += fmt.Sprintf("%s  %s%s\n", cDim, detail, cReset)
	}
	return header + "\n"
}

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

func formatDone(isError bool) string {
	ts := time.Now().Format("15:04:05")
	if isError {
		return fmt.Sprintf("\n%s━━ ✗ FAILED ━━━━━━━━━━━━━━━━━━━%s\n\n", cRed+cBold, cReset)
	}
	return fmt.Sprintf("\n%s━━ ✓ done · %s ━━━━━━━━━━━━━━━━━%s\n\n",
		cGray, ts, cReset)
}

func stripAnsi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	const (
		statePlain = iota
		stateEsc
		stateCsi
		stateDcs
		stateDcsEsc
		stateOsc
		stateOscEsc
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

				st = stateOsc
			} else if c == 'P' || c == '_' || c == '^' {

				st = stateDcs
			} else {
				st = statePlain
				b.WriteRune(c)
			}
		case stateCsi:
			if c >= 0x40 && c <= 0x7E {
				st = statePlain
			}
		case stateDcs:

			if c == 0x1b {
				st = stateDcsEsc
			}
		case stateDcsEsc:
			if c == '\\' {
				st = statePlain
			} else {
				st = stateDcs
			}
		case stateOsc:
			if c == '\x07' {
				st = statePlain
			} else if c == '\x1b' {
				st = stateOscEsc
			}
		case stateOscEsc:
			if c == '\\' {
				st = statePlain
			} else {
				st = stateOsc
			}
		}
	}
	return b.String()
}

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
