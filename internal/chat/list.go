package chat

import (
	"strings"
	"sync"
)

// List is a virtual-scrolling list of Items.
// Only items visible in the current viewport are rendered.
type List struct {
	mu         sync.RWMutex
	items      []Item
	offsetIdx  int // index of first visible item
	offsetLine int // line offset within the first visible item
	width      int
	height     int
	follow     bool // auto-scroll to bottom
	dirty      bool
}

// NewList creates a new virtual list with the given dimensions.
func NewList(width, height int) *List {
	return &List{
		width:  width,
		height: height,
		follow: true,
	}
}

// Append adds items to the end of the list.
func (l *List) Append(items ...Item) {
	l.mu.Lock()
	l.items = append(l.items, items...)
	l.dirty = true
	if l.follow {
		l.scrollToEndLocked()
	}
	l.mu.Unlock()
}

// SetItems replaces all items.
func (l *List) SetItems(items []Item) {
	l.mu.Lock()
	l.items = items
	l.offsetIdx = 0
	l.offsetLine = 0
	l.dirty = true
	if l.follow && len(items) > 0 {
		l.scrollToEndLocked()
	}
	l.mu.Unlock()
}

// ItemAt returns the item at the given index, or nil.
func (l *List) ItemAt(idx int) Item {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if idx < 0 || idx >= len(l.items) {
		return nil
	}
	return l.items[idx]
}

// UpdateItem replaces the item with the given ID.
func (l *List) UpdateItem(id string, item Item) {
	l.mu.Lock()
	for i, it := range l.items {
		if it.ID() == id {
			l.items[i] = item
			l.dirty = true
			break
		}
	}
	l.mu.Unlock()
}

// FindByID returns the item with the given ID, or nil.
func (l *List) FindByID(id string) Item {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, it := range l.items {
		if it.ID() == id {
			return it
		}
	}
	return nil
}

// Len returns the number of items.
func (l *List) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.items)
}

// Height returns the viewport height.
func (l *List) Height() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.height
}

// SetSize updates the viewport dimensions.
func (l *List) SetSize(width, height int) {
	l.mu.Lock()
	l.width = width
	l.height = height
	l.dirty = true
	l.mu.Unlock()
}

// SetFollow enables or disables auto-scroll to bottom.
func (l *List) SetFollow(f bool) {
	l.mu.Lock()
	l.follow = f
	if f {
		l.scrollToEndLocked()
	}
	l.mu.Unlock()
}

// Follow returns whether auto-scroll is active.
func (l *List) Follow() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.follow
}

// ScrollDown scrolls the viewport by n lines.
func (l *List) ScrollDown(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.follow = false
	l.scrollByLocked(n)
}

// ScrollUp scrolls the viewport up by n lines.
func (l *List) ScrollUp(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.follow = false
	l.scrollByLocked(-n)
}

// ScrollToEnd scrolls to the very bottom and re-enables follow.
func (l *List) ScrollToEnd() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.follow = true
	l.scrollToEndLocked()
}

// AtBottom returns whether the viewport is at the bottom.
func (l *List) AtBottom() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.items) == 0 {
		return true
	}
	// Compute total height from offsetIdx to end
	total := 0
	for i := l.offsetIdx; i < len(l.items); i++ {
		total += l.items[i].Height(l.width)
	}
	total -= l.offsetLine
	return total <= l.height
}

// YOffset returns the current scroll position as a line offset from the top.
func (l *List) YOffset() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	offset := 0
	for i := 0; i < l.offsetIdx && i < len(l.items); i++ {
		offset += l.items[i].Height(l.width)
	}
	offset += l.offsetLine
	return offset
}

// Render produces the visible portion of the list as a single string.
// Only items within the viewport are rendered.
// When follow is active, scroll position is re-synced automatically.
func (l *List) Render() string {
	l.mu.Lock()

	// Re-sync scroll when follow is active — item content may have changed
	// since last scroll calculation (e.g. streaming text updates).
	if l.follow {
		l.scrollToEndLocked()
	}

	if len(l.items) == 0 || l.height <= 0 || l.width <= 0 {
		l.mu.Unlock()
		return ""
	}

	var lines []string
	needed := l.height
	idx := l.offsetIdx
	offset := l.offsetLine

	for needed > 0 && idx < len(l.items) {
		content := l.items[idx].Render(l.width)
		itemLines := splitVisualLines(content)

		// Skip lines before the offset
		if offset > 0 && offset < len(itemLines) {
			itemLines = itemLines[offset:]
		} else if offset >= len(itemLines) {
			// Gap/spacing lines
			gap := offset - len(itemLines)
			if gap < needed {
				for i := 0; i < gap; i++ {
					lines = append(lines, "")
				}
				needed -= gap
			}
			idx++
			offset = 0
			continue
		}

		// Take only what fits
		if len(itemLines) > needed {
			itemLines = itemLines[:needed]
		}
		lines = append(lines, itemLines...)
		needed -= len(itemLines)

		// Add 1-line gap between items
		if needed > 0 && idx < len(l.items)-1 {
			lines = append(lines, "")
			needed--
		}

		idx++
		offset = 0
	}

	l.dirty = false

	// Pad with empty lines so the output is always exactly l.height lines.
	// This prevents lipgloss from expanding or contracting the container.
	for len(lines) < l.height {
		lines = append(lines, "")
	}

	result := strings.Join(lines, "\n")
	l.mu.Unlock()
	return result
}

// scrollToEndLocked moves the viewport to show the last items.
// Caller must hold the lock.
func (l *List) scrollToEndLocked() {
	if len(l.items) == 0 {
		l.offsetIdx = 0
		l.offsetLine = 0
		return
	}
	l.offsetIdx, l.offsetLine = l.calcEndPositionLocked()
	l.dirty = true
}

// scrollByLocked scrolls by n lines (positive = down, negative = up).
// Caller must hold the lock.
func (l *List) scrollByLocked(n int) {
	if len(l.items) == 0 {
		return
	}
	if n > 0 {
		// Scroll down
		for n > 0 && l.offsetIdx < len(l.items) {
			itemHeight := l.items[l.offsetIdx].Height(l.width)
			remaining := itemHeight - l.offsetLine + 1 // +1 for gap
			if n >= remaining {
				n -= remaining
				l.offsetIdx++
				l.offsetLine = 0
			} else {
				l.offsetLine += n
				n = 0
			}
		}
		// Clamp: prevent scrolling past the point where the last item
		// would be at the bottom of the viewport. Without this, rapid
		// scrolling produces blank space below all content.
		l.clampMaxScrollLocked()
	} else if n < 0 {
		// Scroll up
		n = -n
		for n > 0 {
			if l.offsetLine > 0 {
				move := min(n, l.offsetLine)
				l.offsetLine -= move
				n -= move
			} else if l.offsetIdx > 0 {
				l.offsetIdx--
				itemHeight := l.items[l.offsetIdx].Height(l.width) + 1
				l.offsetLine = max(0, itemHeight-min(n, itemHeight))
				n -= min(n, itemHeight)
			} else {
				break
			}
		}
	}
	l.dirty = true
}

// clampMaxScrollLocked ensures the scroll position doesn't go past the
// "end" position — i.e. the last item must be at least partially visible
// at the bottom of the viewport. This prevents blank space below content.
// It uses the same backward-walk as scrollToEndLocked but as a maximum bound.
// Caller must hold the lock.
func (l *List) clampMaxScrollLocked() {
	if len(l.items) == 0 || l.height <= 0 {
		return
	}

	// Calculate the maximum scroll position (same as scrollToEndLocked).
	maxIdx, maxLine := l.calcEndPositionLocked()

	// Compare current position with max position.
	// Positions are compared as (offsetIdx, offsetLine) tuples.
	if l.offsetIdx > maxIdx || (l.offsetIdx == maxIdx && l.offsetLine > maxLine) {
		l.offsetIdx = maxIdx
		l.offsetLine = maxLine
	}
}

// calcEndPositionLocked returns the (offsetIdx, offsetLine) that places
// the last item at the bottom of the viewport. Caller must hold the lock.
func (l *List) calcEndPositionLocked() (idx, line int) {
	if len(l.items) == 0 {
		return 0, 0
	}

	// Strategy: compute total content height from each candidate start
	// position and find the one that fills exactly l.height lines with
	// the last item's last line at the bottom.

	// Walk backward from the last item, accumulating height (including
	// 1-line gaps between items) until we exceed l.height.
	remaining := l.height
	idx = len(l.items) - 1
	for idx >= 0 {
		itemH := l.items[idx].Height(l.width)
		totalH := itemH
		if idx < len(l.items)-1 {
			totalH++ // gap line
		}
		if remaining-totalH < 0 {
			break
		}
		remaining -= totalH
		idx--
	}

	if idx < 0 {
		return 0, 0
	}

	itemH := l.items[idx].Height(l.width)
	if idx < len(l.items)-1 {
		// remaining is the space left after fitting items [idx+1..last].
		// We need to fit: visible_from_idx + gap(1) + items_below = l.height.
		// So visible_from_idx = remaining - 1(gap).
		// If remaining <= 1, there's no room for item content, only the gap
		// (or less). In that case skip the item entirely.
		if remaining <= 1 {
			return idx + 1, 0
		}
		visible := remaining - 1
		line = itemH - visible
	} else {
		line = itemH - l.height
	}
	if line < 0 {
		line = 0
	}
	return idx, line
}

// splitVisualLines splits rendered content into visual lines, trimming the
// trailing empty element that strings.Split produces from a trailing "\n".
// This keeps the line count consistent with measureHeight, which subtracts 1
// for a trailing newline. Without this, Height() and Render() disagree on the
// number of lines, causing scroll-position miscalculation.
func splitVisualLines(s string) []string {
	lines := strings.Split(s, "\n")
	// strings.Split("a\n", "\n") produces ["a", ""] — the trailing empty
	// element has no visual presence. measureHeight also subtracts 1 for a
	// trailing newline, so strip exactly one trailing empty element when the
	// string ends with "\n".
	if len(lines) > 0 && strings.HasSuffix(s, "\n") && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// measureHeight("") returns 1 (an empty string is one visual line).
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
