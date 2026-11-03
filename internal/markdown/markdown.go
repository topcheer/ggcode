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
)

var (
	mu    sync.Mutex
	cache = map[int]*glamour.TermRenderer{}
)

func Warmup() {
	_ = Renderer(80)
	_ = Renderer(120)
}

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

func Render(text string, wrap int) string {

	if strings.ContainsRune(text, '\r') {
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
	}
	text = strings.ReplaceAll(text, "\t", "    ")
	text = Normalize(text)
	renderer := Renderer(wrap)
	if renderer == nil {
		return text
	}
	out, err := renderer.Render(text)
	if err != nil {
		return text
	}

	return strings.TrimRight(strings.TrimLeft(out, " \t\n"), " \t\n")
}

func StyleConfig() ansi.StyleConfig {
	if !term.IsTerminal(os.Stdout.Fd()) {
		return glamourstyles.NoTTYStyleConfig
	}
	dark := hasDarkBackground()
	return StyleConfigForDarkMode(dark)
}

func hasDarkBackground() bool {
	fgbg := os.Getenv("COLORFGBG")
	if fgbg == "" {

		return true
	}
	parts := strings.Split(fgbg, ";")
	if len(parts) < 2 {
		return true
	}
	bg := parts[len(parts)-1]

	switch bg {
	case "0", "8", "16", "232", "233", "234", "235", "236", "237", "238", "239":
		return true
	case "7", "15", "231", "255", "254", "253":
		return false
	default:
		return true
	}
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

func Normalize(text string) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	inFence := false
	out := make([]string, 0, len(lines)+4)
	prevWasNormalizedHeading := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if !inFence && isBareFence(trimmed) {
				line = line + "text"
			}
			inFence = !inFence
			out = append(out, line)
			prevWasNormalizedHeading = false
			continue
		}
		if inFence {
			out = append(out, line)
			prevWasNormalizedHeading = false
			continue
		}
		normalized, isHeading := normalizeHeading(line)
		if isHeading && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		} else if prevWasNormalizedHeading && trimmed != "" && !isHeading && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, normalized)
		prevWasNormalizedHeading = isHeading
		if i == len(lines)-1 {
			prevWasNormalizedHeading = false
		}
	}
	return strings.Join(out, "\n")
}

func isBareFence(line string) bool {
	return line == "```" || line == "~~~"
}

func normalizeHeading(line string) (string, bool) {
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
		return line, false
	}
	content := strings.TrimSpace(rest[level:])

	if n := len(content); n > 0 {
		j := n
		for j > 0 && content[j-1] == '#' {
			j--
		}
		switch {
		case j == n:

			content = strings.TrimRight(content, " \t")
		case j == 0:

		default:
			if c := content[j-1]; c == ' ' || c == '\t' {

				content = strings.TrimRight(content[:j-1], " \t")
			}

		}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return line, false
	}

	return strings.Repeat("#", level) + " " + content, true
}
