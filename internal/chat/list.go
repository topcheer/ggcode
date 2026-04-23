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

// Follow returns whether auto-scroll is enabled.
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

// Render produces the visible portion of the list as a single string.
// Only items within the viewport are rendered.
func (l *List) Render() string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.items) == 0 || l.height <= 0 || l.width <= 0 {
		return ""
	}

	var lines []string
	needed := l.height
	idx := l.offsetIdx
	offset := l.offsetLine

	for needed > 0 && idx < len(l.items) {
		content := l.items[idx].Render(l.width)
		itemLines := strings.Split(content, "\n")

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
	return strings.Join(lines, "\n")
}

// scrollToEndLocked moves the viewport to show the last items.
// Caller must hold the lock.
func (l *List) scrollToEndLocked() {
	if len(l.items) == 0 {
		l.offsetIdx = 0
		l.offsetLine = 0
		return
	}

	// Walk backward from the end to find which item starts the last page
	remaining := l.height
	idx := len(l.items) - 1
	for idx >= 0 {
		h := l.items[idx].Height(l.width) + 1 // +1 for gap
		if remaining-h < 0 {
			break
		}
		remaining -= h
		idx--
	}
	l.offsetIdx = idx + 1
	l.offsetLine = 0
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
