package tui

import (
	"github.com/topcheer/ggcode/internal/markdown"
	"github.com/topcheer/ggcode/internal/safego"
)

// RenderMarkdown renders markdown text to ANSI at default width.
func RenderMarkdown(text string) string {
	return RenderMarkdownWidth(text, 80)
}

// RenderMarkdownWidth renders markdown text to ANSI at the given width.
func RenderMarkdownWidth(text string, wrap int) string {
	return markdown.Render(text, wrap)
}

// prewarmMarkdownRenderers warms up the markdown renderer cache in the background.
func prewarmMarkdownRenderers(widths ...int) {
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
	safego.Go("tui.markdown.warmRenderer", func() {
		for _, width := range warmWidths {
			_ = markdown.Renderer(width)
		}
	})
}
