package chat

import (
	"fmt"
	"strings"
	"testing"
)

// Diagnostic: how many blocks actually get reused across streaming updates?
func TestStreamingBlockReuse(t *testing.T) {
	base := strings.Repeat("Long markdown **paragraph** with `code` and lists\n\n- a\n- b\n", 700)
	cache := streamRenderCache{}
	prevBlocks := 0
	for step := 0; step < 5; step++ {
		text := base + strings.Repeat("tail ", step)
		blocks := splitMarkdownBlocks(normalizeStreamingMarkdown(text))
		reuse := 0
		if cache.width == 120 && strings.HasPrefix(normalizeStreamingMarkdown(text), cache.source) {
			for reuse < len(cache.blocks) && reuse < len(blocks) && cache.blocks[reuse] == blocks[reuse] {
				reuse++
			}
		}
		fmt.Printf("step=%d blocks=%d prevBlocks=%d reuse=%d\n", step, len(blocks), prevBlocks, reuse)
		_, cache = renderStreamingMarkdown(text, 120, &cache)
		prevBlocks = len(blocks)
	}
}
