package context

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/topcheer/ggcode/internal/debug"
)

const (
	maxPinnedItems = 10
	maxPinnedChars = 2000
	maxPinnedTotal = 8000

	pinnedMarker = "[Pinned Context - protected from compaction]"
)

type PinnedItem struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type PinnedContext struct {
	mu    sync.Mutex
	items []PinnedItem
}

func newPinnedContext() *PinnedContext {
	return &PinnedContext{}
}

func (p *PinnedContext) Add(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("cannot pin empty text")
	}
	if utf8.RuneCountInString(text) > maxPinnedChars {
		text = string([]rune(text)[:maxPinnedChars])
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.items) >= maxPinnedItems {
		return "", fmt.Errorf("maximum %d pinned items reached, remove one first", maxPinnedItems)
	}

	totalRunes := 0
	for _, item := range p.items {
		totalRunes += utf8.RuneCountInString(item.Text)
	}
	itemRunes := utf8.RuneCountInString(text)
	if totalRunes+itemRunes > maxPinnedTotal {
		remaining := maxPinnedTotal - totalRunes
		return "", fmt.Errorf("pinned context budget exceeded (%d chars remaining of %d)", remaining, maxPinnedTotal)
	}

	item := PinnedItem{
		ID:        "pin_" + uuid.New().String()[:8],
		Text:      text,
		CreatedAt: time.Now(),
	}
	p.items = append(p.items, item)
	debug.Log("ctx", "pinned context: added item %s (%d chars, %d total items)", item.ID, len(text), len(p.items))
	return item.ID, nil
}

func (p *PinnedContext) Remove(idOrIndex string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	var idx int = -1
	if n, err := fmtAtoi(idOrIndex); err == nil && n >= 1 && n <= len(p.items) {
		idx = n - 1
	} else {
		for i, item := range p.items {
			if strings.HasPrefix(item.ID, idOrIndex) {
				idx = i
				break
			}
		}
	}

	if idx < 0 {
		return false
	}

	removed := p.items[idx]
	p.items = append(p.items[:idx], p.items[idx+1:]...)
	debug.Log("ctx", "pinned context: removed item %s (%d remaining)", removed.ID, len(p.items))
	return true
}

func (p *PinnedContext) Clear() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.items)
	p.items = nil
	debug.Log("ctx", "pinned context: cleared %d items", n)
	return n
}

func (p *PinnedContext) List() []PinnedItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PinnedItem, len(p.items))
	copy(out, p.items)
	return out
}

func (p *PinnedContext) IsEmpty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.items) == 0
}

func (p *PinnedContext) Render() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.items) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(pinnedMarker)
	sb.WriteString("\n")
	for i, item := range p.items {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, item.Text)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func fmtAtoi(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
