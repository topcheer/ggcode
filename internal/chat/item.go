package chat

import (
	"strings"
)

type Item interface {
	Render(width int) string

	ID() string

	Height(width int) int
}

type CachedItem struct {
	rendered     string
	cachedWidth  int
	cachedHeight int
}

func (c *CachedItem) GetCached(width int) (string, int, bool) {
	if c.cachedWidth == width && c.rendered != "" {
		return c.rendered, c.cachedHeight, true
	}
	return "", 0, false
}

func (c *CachedItem) SetCached(rendered string, width, height int) {
	c.rendered = rendered
	c.cachedWidth = width
	c.cachedHeight = height
}

func (c *CachedItem) Invalidate() {
	c.rendered = ""
	c.cachedWidth = 0
	c.cachedHeight = 0
}

func measureHeight(s string) int {
	if s == "" {
		return 1
	}
	n := strings.Count(s, "\n") + 1
	if strings.HasSuffix(s, "\n") {
		n--
	}
	return n
}

func measureHeightWidth(s string, _ int) int {
	return len(splitVisualLines(s))
}
