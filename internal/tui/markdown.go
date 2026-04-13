package tui

import (
	"os"
	"strings"
	"sync"
	"unicode"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

var (
	rendererMu          sync.Mutex
	rendererCache       = map[int]*glamour.TermRenderer{}
	rendererPrewarmOnce sync.Once
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
		glamour.WithStyles(markdownStyleConfig()),
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

func prewarmMarkdownRenderers(widths ...int) {
	rendererPrewarmOnce.Do(func() {
		warmWidths := make([]int, 0, len(widths))
		seen := make(map[int]struct{}, len(widths))
		for _, width := range widths {
			if width <= 0 {
				continue
			}
			if _, ok := seen[width]; ok {
				continue
			}
			seen[width] = struct{}{}
			warmWidths = append(warmWidths, width)
		}
		if len(warmWidths) == 0 {
			return
		}
		go func() {
			for _, width := range warmWidths {
				_ = rendererForWidth(width)
			}
		}()
	})
}

func markdownStyleConfig() ansi.StyleConfig {
	if !term.IsTerminal(os.Stdout.Fd()) {
		return glamourstyles.NoTTYStyleConfig
	}
	dark := termenv.HasDarkBackground()
	return markdownStyleConfigForDarkMode(dark)
}

func markdownStyleConfigForDarkMode(dark bool) ansi.StyleConfig {
	var cfg ansi.StyleConfig
	if dark {
		cfg = glamourstyles.DarkStyleConfig
	} else {
		cfg = glamourstyles.LightStyleConfig
	}

	if dark {
		applyMarkdownCodePalette(&cfg, "#7aa2f7", "#a9b1d6", "#565f89", "#9ece6a", "#e0af68")
	} else {
		applyMarkdownCodePalette(&cfg, "#005f87", "#334155", "#64748b", "#2f855a", "#975a16")
	}
	return cfg
}

func applyMarkdownCodePalette(cfg *ansi.StyleConfig, accent, text, comment, success, stringColor string) {
	if cfg == nil {
		return
	}
	cfg.Code.Color = stringPtr(accent)
	cfg.Code.BackgroundColor = nil
	cfg.CodeBlock.Color = stringPtr(text)
	if cfg.CodeBlock.Chroma == nil {
		return
	}
	cfg.CodeBlock.Chroma.Text.Color = stringPtr(text)
	cfg.CodeBlock.Chroma.Error.BackgroundColor = nil
	cfg.CodeBlock.Chroma.Comment.Color = stringPtr(comment)
	cfg.CodeBlock.Chroma.CommentPreproc.Color = stringPtr(accent)
	cfg.CodeBlock.Chroma.Keyword.Color = stringPtr(accent)
	cfg.CodeBlock.Chroma.KeywordReserved.Color = stringPtr(accent)
	cfg.CodeBlock.Chroma.KeywordNamespace.Color = stringPtr(accent)
	cfg.CodeBlock.Chroma.Operator.Color = stringPtr(accent)
	cfg.CodeBlock.Chroma.Punctuation.Color = stringPtr(text)
	cfg.CodeBlock.Chroma.Name.Color = stringPtr(text)
	cfg.CodeBlock.Chroma.NameTag.Color = stringPtr(accent)
	cfg.CodeBlock.Chroma.NameAttribute.Color = stringPtr(success)
	cfg.CodeBlock.Chroma.NameFunction.Color = stringPtr(success)
	cfg.CodeBlock.Chroma.LiteralString.Color = stringPtr(stringColor)
	cfg.CodeBlock.Chroma.LiteralStringEscape.Color = stringPtr(accent)
}

func stringPtr(v string) *string {
	return &v
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
			if !inFence && isBareMarkdownFence(trimmed) {
				lines[i] = line + "text"
			}
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

func isBareMarkdownFence(line string) bool {
	return line == "```" || line == "~~~"
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
