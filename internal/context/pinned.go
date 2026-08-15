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

// Context Pinning - protect critical context from compaction loss.
//
// Research basis: Cursor's "pinned context", Claude Code's persistent context
// directives, and Cline's "@context" all address the same problem: after
// context compaction, important early constraints (build flags, test configs,
// auth details, project conventions) get blurred into the summary and lost.
//
// Users pin critical context with /pin <text>. Pinned items are injected as a
// dedicated system message that:
//   - Appears right after the compaction summary (survives compaction)
//   - Is re-injected on every compaction cycle
//   - Is capped to a small budget so it never dominates the context window
//
// Budget limits (from context-pinning-design.md):
//   - Max 10 items
//   - Max 2000 chars per item
//   - Max 8000 chars total (roughly 2000 tokens)

const (
	maxPinnedItems = 10
	maxPinnedChars = 2000 // per item
	maxPinnedTotal = 8000 // all items combined

	// pinnedMarker is a sentinel string embedded in the pinned system message
	// so we can locate and update/replace it without scanning item content.
	pinnedMarker = "[Pinned Context - protected from compaction]"
)

// PinnedItem represents a single piece of user-pinned context.
type PinnedItem struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// PinnedContext stores user-pinned context items that survive compaction.
type PinnedContext struct {
	mu    sync.Mutex
	items []PinnedItem
}

// newPinnedContext creates an empty PinnedContext.
func newPinnedContext() *PinnedContext {
	return &PinnedContext{}
}

// Add pins a piece of context text and returns its ID.
// Returns an error if the maximum number of items is reached.
func (p *PinnedContext) Add(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("cannot pin empty text")
	}
	// Truncate by RUNES, not bytes: maxPinnedChars counts characters, and a
	// byte slice could cut a multi-byte CJK/emoji rune in half, producing
	// invalid UTF-8 that flows straight into the system message (#386).
	if utf8.RuneCountInString(text) > maxPinnedChars {
		text = string([]rune(text)[:maxPinnedChars])
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.items) >= maxPinnedItems {
		return "", fmt.Errorf("maximum %d pinned items reached, remove one first", maxPinnedItems)
	}

	totalLen := 0
	for _, item := range p.items {
		totalLen += len(item.Text)
	}
	if totalLen+len(text) > maxPinnedTotal {
		remaining := maxPinnedTotal - totalLen
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

// Remove deletes a pinned item by its 1-based index or ID prefix.
func (p *PinnedContext) Remove(idOrIndex string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Try numeric index (1-based).
	var idx int = -1
	if n, err := fmtAtoi(idOrIndex); err == nil && n >= 1 && n <= len(p.items) {
		idx = n - 1
	} else {
		// Try ID prefix match.
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

// Clear removes all pinned items.
func (p *PinnedContext) Clear() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.items)
	p.items = nil
	debug.Log("ctx", "pinned context: cleared %d items", n)
	return n
}

// List returns a copy of all pinned items.
func (p *PinnedContext) List() []PinnedItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PinnedItem, len(p.items))
	copy(out, p.items)
	return out
}

// IsEmpty returns true if no items are pinned.
func (p *PinnedContext) IsEmpty() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.items) == 0
}

// Render returns the pinned context formatted for injection as a system
// message. Returns empty string if no items are pinned.
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

// fmtAtoi is a thin wrapper to avoid importing strconv just for one call.
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
