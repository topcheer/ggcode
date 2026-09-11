package tui

import (
	"github.com/topcheer/ggcode/internal/markdown"
	"github.com/topcheer/ggcode/internal/safego"
)

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
