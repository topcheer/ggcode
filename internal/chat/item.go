package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Item is the core interface for any renderable element in the conversation.
type Item interface {
	// Render produces the ANSI-styled string for this item at the given width.
	Render(width int) string

	// ID returns a unique identifier for deduplication and scroll targeting.
	ID() string

	// Height returns the number of visual lines this item occupies at the given width.
	// Used by the virtual list to compute scroll positions without full rendering.
	Height(width int) int
}

// CachedItem provides common caching for items whose rendering is expensive.
// Embed in concrete item types and call GetCached/SetCached/Invalidate.
type CachedItem struct {
	rendered     string
	cachedWidth  int
	cachedHeight int
}

// GetCached returns the cached render and height if the width matches.
// Returns ("", 0, false) on cache miss.
func (c *CachedItem) GetCached(width int) (string, int, bool) {
	if c.cachedWidth == width && c.rendered != "" {
		return c.rendered, c.cachedHeight, true
	}
	return "", 0, false
}

// SetCached stores the rendered output and its height.
func (c *CachedItem) SetCached(rendered string, width, height int) {
	c.rendered = rendered
	c.cachedWidth = width
	c.cachedHeight = height
}

// Invalidate clears the cache, forcing re-render on next access.
func (c *CachedItem) Invalidate() {
	c.rendered = ""
	c.cachedWidth = 0
	c.cachedHeight = 0
}

// measureHeight counts the visual lines in a rendered string, ignoring
// terminal width. Each \n-separated segment counts as exactly one line
// regardless of how long it is. Use measureHeightWidth when you need to
// account for wrapping at a specific terminal width.
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

// measureHeightWidth counts visual lines in a rendered string at the given
// terminal width. Unlike measureHeight, it accounts for line wrapping: when
// a \n-separated line's visual width exceeds the available width, it is
// counted as ceil(visualWidth / width) lines. ANSI escape sequences are
// correctly handled via lipgloss.Width.
func measureHeightWidth(s string, width int) int {
	if s == "" {
		return 1
	}
	if width <= 0 {
		return measureHeight(s)
	}
	lines := strings.Split(s, "\n")
	// A trailing "\n" produces a spurious empty element with no visual presence.
	if len(lines) > 0 && strings.HasSuffix(s, "\n") && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := 0
	for _, line := range lines {
		vw := lipgloss.Width(line)
		if vw == 0 {
			total++
			continue
		}
		wrapped := (vw + width - 1) / width // ceil division
		if wrapped < 1 {
			wrapped = 1
		}
		total += wrapped
	}
	if total == 0 {
		return 1
	}
	return total
}
