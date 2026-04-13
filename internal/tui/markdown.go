package tui

import (
	"strings"
	"sync"
	"unicode"

	"github.com/charmbracelet/glamour"
)

var (
	rendererMu    sync.Mutex
	rendererCache = map[int]*glamour.TermRenderer{}
)

func rendererForWidth(wrap int) *glamour.TermRenderer {
	if wrap <= 0 {
		wrap = 80
	}
	rendererMu.Lock()
	defer rendererMu.Unlock()
	if renderer, ok := rendererCache[wrap]; ok {
		return renderer
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		rendererCache[wrap] = nil
		return nil
	}
	rendererCache[wrap] = renderer
	return renderer
}

func RenderMarkdown(text string) string {
	return RenderMarkdownWidth(text, 80)
}

func RenderMarkdownWidth(text string, wrap int) string {
	text = normalizeTerminalMarkdown(text)
	renderer := rendererForWidth(wrap)
	if renderer == nil {
		return text
	}
	out, err := renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, " \t\n")
}

func normalizeTerminalMarkdown(text string) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[i] = normalizeTerminalMarkdownHeading(line)
	}
	return strings.Join(lines, "\n")
}

func normalizeTerminalMarkdownHeading(line string) string {
	indentLen := 0
	for indentLen < len(line) && indentLen < 3 && line[indentLen] == ' ' {
		indentLen++
	}
	rest := line[indentLen:]
	level := 0
	for level < len(rest) && level < 6 && rest[level] == '#' {
		level++
	}
	if level == 0 || level >= len(rest) || !unicode.IsSpace(rune(rest[level])) {
		return line
	}
	content := strings.TrimSpace(rest[level:])
	content = strings.TrimRight(content, "#")
	content = strings.TrimSpace(content)
	if content == "" {
		return line
	}
	return strings.Repeat(" ", indentLen) + content
}
