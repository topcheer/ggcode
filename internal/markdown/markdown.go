package markdown

import (
	"os"
	"strings"
	"sync"
	"unicode"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

var (
	mu    sync.Mutex
	cache = map[int]*glamour.TermRenderer{}
)

// Renderer returns a cached glamour.TermRenderer for the given wrap width.
func Renderer(wrap int) *glamour.TermRenderer {
	if wrap <= 0 {
		wrap = 80
	}
	mu.Lock()
	defer mu.Unlock()
	if r, ok := cache[wrap]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(StyleConfig()),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		cache[wrap] = nil
		return nil
	}
	cache[wrap] = r
	return r
}

// Render renders markdown text to ANSI with the given wrap width.
func Render(text string, wrap int) string {
	text = Normalize(text)
	renderer := Renderer(wrap)
	if renderer == nil {
		return text
	}
	out, err := renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(out, " \t\n")
}

// StyleConfig returns the glamour style config adapted for dark/light terminal.
func StyleConfig() ansi.StyleConfig {
	if !term.IsTerminal(os.Stdout.Fd()) {
		return glamourstyles.NoTTYStyleConfig
	}
	dark := termenv.HasDarkBackground()
	return StyleConfigForDarkMode(dark)
}

func StyleConfigForDarkMode(dark bool) ansi.StyleConfig {
	var cfg ansi.StyleConfig
	if dark {
		cfg = glamourstyles.DarkStyleConfig
	} else {
		cfg = glamourstyles.LightStyleConfig
	}
	if dark {
		applyPalette(&cfg, "#7aa2f7", "#a9b1d6", "#565f89", "#9ece6a", "#e0af68")
	} else {
		applyPalette(&cfg, "#005f87", "#334155", "#64748b", "#2f855a", "#975a16")
	}
	return cfg
}

func applyPalette(cfg *ansi.StyleConfig, accent, text, comment, success, stringColor string) {
	if cfg == nil {
		return
	}
	cfg.Code.Color = &accent
	cfg.Code.BackgroundColor = nil
	cfg.CodeBlock.Color = &text
	if cfg.CodeBlock.Chroma == nil {
		return
	}
	cfg.CodeBlock.Chroma.Text.Color = &text
	cfg.CodeBlock.Chroma.Error.BackgroundColor = nil
	cfg.CodeBlock.Chroma.Comment.Color = &comment
	cfg.CodeBlock.Chroma.CommentPreproc.Color = &accent
	cfg.CodeBlock.Chroma.Keyword.Color = &accent
	cfg.CodeBlock.Chroma.KeywordReserved.Color = &accent
	cfg.CodeBlock.Chroma.KeywordNamespace.Color = &accent
	cfg.CodeBlock.Chroma.Operator.Color = &accent
	cfg.CodeBlock.Chroma.Punctuation.Color = &text
	cfg.CodeBlock.Chroma.Name.Color = &text
	cfg.CodeBlock.Chroma.NameTag.Color = &accent
	cfg.CodeBlock.Chroma.NameAttribute.Color = &success
	cfg.CodeBlock.Chroma.NameFunction.Color = &success
	cfg.CodeBlock.Chroma.LiteralString.Color = &stringColor
	cfg.CodeBlock.Chroma.LiteralStringEscape.Color = &accent
}

// Normalize cleans up markdown for terminal rendering.
func Normalize(text string) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if !inFence && isBareFence(trimmed) {
				lines[i] = line + "text"
			}
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		lines[i] = normalizeHeading(line)
	}
	return strings.Join(lines, "\n")
}

func isBareFence(line string) bool {
	return line == "```" || line == "~~~"
}

func normalizeHeading(line string) string {
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
