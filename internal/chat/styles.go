package chat

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

type ToolStatus int

const (
	StatusPending ToolStatus = iota
	StatusRunning
	StatusSuccess
	StatusError
	StatusCanceled
)

func (s ToolStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusSuccess:
		return "success"
	case StatusError:
		return "error"
	case StatusCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}

type Styles struct {
	UserPrefix string
	UserIcon   string
	UserStyle  lipgloss.Style

	AssistantPrefix string
	AssistantIcon   string
	AssistantStyle  lipgloss.Style

	ToolName lipgloss.Style

	ToolBody lipgloss.Style
	BashBody lipgloss.Style

	SystemPrefix string
	SystemStyle  lipgloss.Style

	ReasoningPrefix string
	ReasoningStyle  lipgloss.Style

	ErrorStyle lipgloss.Style

	MutedStyle lipgloss.Style

	ItemGap int
}

func DefaultStyles() Styles {
	return Styles{
		UserPrefix:      "❯ ",
		UserIcon:        "❯",
		UserStyle:       lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		AssistantPrefix: "● ",
		AssistantIcon:   "●",
		AssistantStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("81")),
		ToolName:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		ToolBody:        lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		BashBody:        lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("235")),
		SystemPrefix:    "○ ",
		SystemStyle:     lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		ReasoningPrefix: lipgloss.NewStyle().Foreground(lipgloss.Color("183")).Render("✦ "),
		ReasoningStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("147")),
		ErrorStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		MutedStyle:      lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		ItemGap:         1,
	}
}

var toolAnimGlyphs = []string{"◐", "◓", "◑", "◒"}

var toolAnimFrame int

func SetToolAnimFrame(frame int) {
	toolAnimFrame = frame
}

func (s Styles) ToolIcon(status ToolStatus) string {
	switch status {
	case StatusPending:
		return "○"
	case StatusRunning:
		return toolAnimGlyphs[toolAnimFrame%len(toolAnimGlyphs)]
	case StatusSuccess:
		return "●"
	case StatusError:
		return "●"
	case StatusCanceled:
		return "⊘"
	default:
		return "?"
	}
}

func (s Styles) ToolIconStyle(status ToolStatus) string {
	icon := s.ToolIcon(status)
	switch status {
	case StatusSuccess:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(icon)
	case StatusError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(icon)
	case StatusPending, StatusRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(icon)
	case StatusCanceled:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render(icon)
	default:
		return icon
	}
}

const toolHeaderMaxRenderWidth = 120

func (s Styles) ToolHeader(status ToolStatus, name string, width int, params ...string) string {
	icon := s.ToolIconStyle(status)
	paramStr := strings.Join(params, " ")

	prefix, prefixW := s.toolHeaderPrefix(icon, name, width)
	paramStr = capHeaderParams(paramStr, prefixW)

	fullLine := prefix + paramStr
	if lipgloss.Width(fullLine) <= width {
		return fullLine
	}
	return wrapHeaderParams(prefix, paramStr, prefixW, width)
}

const toolNameTruncSuffix = "..."

func truncateRunesByWidth(s string, maxW int) int {
	w := 0
	for i, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxW {
			return i
		}
		w += rw
	}
	return len(s)
}

func (s Styles) toolHeaderPrefix(icon, name string, width int) (prefix string, prefixW int) {
	prefix = fmt.Sprintf("%s %s ", icon, s.ToolName.Render(name))
	prefixW = lipgloss.Width(prefix)
	if prefixW <= width || width <= 0 {
		return prefix, prefixW
	}

	avail := width - lipgloss.Width(icon) - 3
	if avail <= len(toolNameTruncSuffix) {

		prefix = strings.TrimSuffix(icon, " ")
		if lipgloss.Width(prefix) > width {
			prefix = ""
		}
		return prefix, lipgloss.Width(prefix)
	}
	avail -= len(toolNameTruncSuffix)
	cut := truncateRunesByWidth(name, avail)
	short := name[:cut]
	if cut < len(name) {
		short += toolNameTruncSuffix
	}
	prefix = fmt.Sprintf("%s %s ", icon, s.ToolName.Render(short))
	return prefix, lipgloss.Width(prefix)
}

func capHeaderParams(paramStr string, prefixW int) string {
	if lipgloss.Width(paramStr)+prefixW <= toolHeaderMaxRenderWidth {
		return paramStr
	}
	avail := toolHeaderMaxRenderWidth - prefixW - 1
	if avail < 10 {
		avail = 10
	}

	cut := truncateRunesByWidth(paramStr, avail)
	return paramStr[:cut] + "…"
}

func wrapHeaderParams(prefix, paramStr string, prefixW, width int) string {
	indent := strings.Repeat(" ", prefixW)
	var lines []string
	remaining := paramStr
	first := true
	for remaining != "" {
		linePrefix := indent
		if first {
			linePrefix = prefix
			first = false
		}
		avail := width - prefixW
		if avail < 1 {
			avail = 1
		}
		fit, rest := splitAtWidth(remaining, avail)
		lines = append(lines, linePrefix+fit)
		remaining = rest
	}
	return strings.Join(lines, "\n")
}

func splitAtWidth(s string, maxW int) (string, string) {
	if lipgloss.Width(s) <= maxW {
		return s, ""
	}

	runes := []rune(s)
	totalW := 0
	breakIdx := len(runes)
	spaceIdx := -1
	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		if totalW+rw > maxW && i > 0 {
			breakIdx = i
			break
		}
		totalW += rw
		if r == ' ' {
			spaceIdx = i
		}
	}

	if spaceIdx > 0 && spaceIdx < breakIdx {
		return string(runes[:spaceIdx]), string(runes[spaceIdx+1:])
	}
	return string(runes[:breakIdx]), string(runes[breakIdx:])
}

func truncateTailByWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}

	runes := []rune(s)

	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if lipgloss.Width(string(runes)) <= maxW {
			return strings.TrimRight(string(runes), "\x1b")
		}
	}
	return ""
}

const tabStop = 4

func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var sb strings.Builder
	col := 0
	for _, r := range s {
		switch r {
		case '\t':
			n := tabStop - col%tabStop
			for i := 0; i < n; i++ {
				sb.WriteByte(' ')
			}
			col += n
		case '\n':
			sb.WriteRune(r)
			col = 0
		default:
			sb.WriteRune(r)
			col++
		}
	}
	return sb.String()
}

const ToolBodyMaxLines = 10

func FormatBody(content string, width int, maxLines int) (string, bool) {
	if content == "" {
		return "", false
	}

	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	content = expandTabs(content)

	var wrapped []string
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		if lipgloss.Width(line) <= width {
			wrapped = append(wrapped, line)
			continue
		}

		runes := []rune(line)
		for len(runes) > 0 {
			cut := 0
			for cut < len(runes) && lipgloss.Width(string(runes[:cut+1])) <= width {
				cut++
			}
			if cut == 0 {
				cut = 1
			}

			chunk := string(runes[:cut])
			if spaceIdx := strings.LastIndex(chunk, " "); spaceIdx > 0 {
				runeIdx := utf8.RuneCountInString(chunk[:spaceIdx])
				wrapped = append(wrapped, string(runes[:runeIdx]))
				runes = []rune(strings.TrimLeft(string(runes[runeIdx:]), " "))
			} else {
				wrapped = append(wrapped, chunk)
				runes = runes[cut:]
			}
		}
	}

	truncated := false
	if maxLines > 0 && len(wrapped) > maxLines {
		truncated = true
		hidden := len(wrapped) - maxLines
		wrapped = wrapped[len(wrapped)-maxLines:]

		wrapped = append([]string{truncationHint(hidden, width)}, wrapped...)
	}

	return strings.Join(wrapped, "\n"), truncated
}
