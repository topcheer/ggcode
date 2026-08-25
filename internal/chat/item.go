package chat

import (
	"strings"
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

// measureHeightWidth returns the number of physical \n-delimited lines in
// the rendered string - EXACTLY what List.Render slices via
// splitVisualLines. Height() and Render() must agree line-for-line:
// List.Render's gap branch turns any "predicted" height above the physical
// line count into visible blank rows (content floating mid-screen with
// empty space below), and any height below it overflows the viewport and
// pushes the panel border off-screen. This has been fixed repeatedly by
// patching producers (CR, tabs, width overflows); counting wrapped rows by
// prediction (the old ceil(visualWidth/width) logic) was the remaining
// structural divergence, so height is now DEFINED as the physical count.
//
// Pre-wrapping is the producers' responsibility: FormatBody, ToolHeader,
// the markdown renderer, and wrapLines all wrap eagerly and are held to
// the width invariant by tool_render_invariant_test.go. The width
// parameter is retained for call-site compatibility.
func measureHeightWidth(s string, _ int) int {
	return len(splitVisualLines(s))
}
