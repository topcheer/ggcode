package chat

import (
	"strings"
	"sync"
)

const DefaultMaxItems = 2000

type List struct {
	mu         sync.RWMutex
	items      []Item
	offsetIdx  int
	offsetLine int
	width      int
	height     int
	follow     bool
	dirty      bool
	maxItems   int
}

func NewList(width, height int) *List {
	return &List{
		width:    width,
		height:   height,
		follow:   true,
		maxItems: DefaultMaxItems,
	}
}

func (l *List) Append(items ...Item) {
	l.mu.Lock()
	l.items = append(l.items, items...)
	l.trimLocked()
	l.dirty = true
	if l.follow {
		l.scrollToEndLocked()
	}
	l.mu.Unlock()
}

func (l *List) SetMaxItems(n int) {
	l.mu.Lock()
	l.maxItems = n
	l.trimLocked()
	l.mu.Unlock()
}

func (l *List) trimLocked() {
	if l.maxItems <= 0 || len(l.items) <= l.maxItems {
		return
	}
	target := l.maxItems * 3 / 4
	drop := len(l.items) - target
	kept := make([]Item, target)
	copy(kept, l.items[drop:])
	l.items = kept
	l.offsetIdx -= drop
	if l.offsetIdx < 0 {
		l.offsetIdx = 0
		l.offsetLine = 0
	}
	l.dirty = true
}

func (l *List) RemoveByID(id string) {
	l.mu.Lock()
	for i, item := range l.items {
		if item.ID() == id {
			l.items = append(l.items[:i], l.items[i+1:]...)
			l.dirty = true
			break
		}
	}
	l.mu.Unlock()
}

func (l *List) UpdateByID(id string, fn func(Item)) {
	l.mu.Lock()
	for _, item := range l.items {
		if item.ID() == id {
			fn(item)
			l.dirty = true
			break
		}
	}
	l.mu.Unlock()
}

func (l *List) SetItems(items []Item) {
	l.mu.Lock()
	l.items = items
	l.offsetIdx = 0
	l.offsetLine = 0
	l.trimLocked()
	l.dirty = true
	if l.follow && len(items) > 0 {
		l.scrollToEndLocked()
	}
	l.mu.Unlock()
}

func (l *List) ItemAt(idx int) Item {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if idx < 0 || idx >= len(l.items) {
		return nil
	}
	return l.items[idx]
}

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

func (l *List) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.items)
}

func (l *List) LastAssistantText() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for i := len(l.items) - 1; i >= 0; i-- {
		if a, ok := l.items[i].(*AssistantItem); ok {
			return a.Text()
		}
	}
	return ""
}

func (l *List) TruncateAfterLastUser() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lastUserIdx := -1
	for i := len(l.items) - 1; i >= 0; i-- {
		if _, ok := l.items[i].(*UserItem); ok {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 || lastUserIdx == len(l.items)-1 {
		return false
	}
	l.items = l.items[:lastUserIdx+1]
	l.dirty = true
	if l.follow {
		l.scrollToEndLocked()
	}
	return true
}

func (l *List) Height() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.height
}

func (l *List) SetSize(width, height int) {
	l.mu.Lock()
	l.width = width
	l.height = height
	l.dirty = true
	l.mu.Unlock()
}

func (l *List) SetFollow(f bool) {
	l.mu.Lock()
	l.follow = f
	if f {
		l.scrollToEndLocked()
	}
	l.mu.Unlock()
}

func (l *List) Follow() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.follow
}

func (l *List) ScrollDown(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.follow = false
	l.scrollByLocked(n)

	endIdx, endLine := l.calcEndPositionLocked()
	if l.offsetIdx >= endIdx && l.offsetLine >= endLine {
		l.follow = true
	}
}

func (l *List) ScrollUp(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.follow = false
	l.scrollByLocked(-n)
}

func (l *List) ScrollToEnd() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.follow = true
	l.scrollToEndLocked()
}

func (l *List) FollowOutput() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.follow {
		return
	}
	l.scrollToEndLocked()
}

func (l *List) AtBottom() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.items) == 0 {
		return true
	}

	total := 0
	for i := l.offsetIdx; i < len(l.items); i++ {
		total += l.items[i].Height(l.width)
	}
	total -= l.offsetLine
	return total <= l.height
}

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

func (l *List) Render() string {
	l.mu.Lock()

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

		if offset > 0 && offset < len(itemLines) {
			itemLines = itemLines[offset:]
		} else if offset >= len(itemLines) {

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

		if len(itemLines) > needed {
			itemLines = itemLines[:needed]
		}
		lines = append(lines, itemLines...)
		needed -= len(itemLines)

		if needed > 0 && idx < len(l.items)-1 {
			lines = append(lines, "")
			needed--
		}

		idx++
		offset = 0
	}

	l.dirty = false

	for len(lines) < l.height {
		lines = append(lines, "")
	}

	result := strings.Join(lines, "\n")
	l.mu.Unlock()
	return result
}

func (l *List) scrollToEndLocked() {
	if len(l.items) == 0 {
		l.offsetIdx = 0
		l.offsetLine = 0
		return
	}
	l.offsetIdx, l.offsetLine = l.calcEndPositionLocked()
	l.dirty = true
}

func (l *List) scrollByLocked(n int) {
	if len(l.items) == 0 {
		return
	}
	if n > 0 {

		for n > 0 && l.offsetIdx < len(l.items) {
			itemHeight := l.items[l.offsetIdx].Height(l.width)
			remaining := itemHeight - l.offsetLine + 1
			if n >= remaining {
				n -= remaining
				l.offsetIdx++
				l.offsetLine = 0
			} else {
				l.offsetLine += n
				n = 0
			}
		}

		l.clampMaxScrollLocked()
	} else if n < 0 {

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

func (l *List) clampMaxScrollLocked() {
	if len(l.items) == 0 || l.height <= 0 {
		return
	}

	maxIdx, maxLine := l.calcEndPositionLocked()

	if l.offsetIdx > maxIdx || (l.offsetIdx == maxIdx && l.offsetLine > maxLine) {
		l.offsetIdx = maxIdx
		l.offsetLine = maxLine
	}
}

func (l *List) calcEndPositionLocked() (idx, line int) {
	if len(l.items) == 0 {
		return 0, 0
	}

	remaining := l.height
	idx = len(l.items) - 1
	for idx >= 0 {
		itemH := l.items[idx].Height(l.width)
		totalH := itemH
		if idx < len(l.items)-1 {
			totalH++
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

func splitVisualLines(s string) []string {
	lines := strings.Split(s, "\n")

	if len(lines) > 0 && strings.HasSuffix(s, "\n") && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}
