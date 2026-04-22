package tui

import (
	"strings"
	"sync"
)

// ChatEntry represents a single visual unit in the conversation panel.
// Raw content is stored and rendered lazily on demand, so that resize /
// sidebar toggle can re-render at the new width without losing fidelity.
type ChatEntry struct {
	// Role classifies the entry for rendering selection:
	//   "user"        — user message, wrapped with wordwrap
	//   "assistant"   — LLM markdown response, rendered via glamour
	//   "system"      — slash command output, status messages (rendered plain)
	//   "tool"        — tool activity block (already formatted)
	//   "compaction"  — compaction status line
	//   "bullet"      — standalone bullet prefix (used during streaming)
	Role string

	// RawText is the original unstyled content. For "assistant" entries
	// this is raw markdown; for "user" entries it is plain text; for
	// "system" / "tool" entries it may already contain lipgloss styles.
	RawText string

	// Style overrides: if set, rendered output uses this styled prefix.
	Prefix string // e.g. "● ", "❯ "

	// Streaming is true while the entry is still receiving chunks.
	// When true, the entry is re-rendered on every View() call.
	Streaming bool

	// cache
	rendered    string
	cachedWidth int
}

// Rendered returns the rendered ANSI text for this entry at the given width.
// Results are cached; re-rendering only happens when the width changes or
// the entry is still streaming.
func (e *ChatEntry) Rendered(width int) string {
	if !e.Streaming && e.cachedWidth == width && e.rendered != "" {
		return e.rendered
	}
	e.rendered = e.render(width)
	e.cachedWidth = width
	return e.rendered
}

// Invalidate clears the render cache, forcing re-render on next access.
func (e *ChatEntry) Invalidate() {
	e.cachedWidth = 0
	e.rendered = ""
}

// Append appends text to RawText and invalidates the cache.
func (e *ChatEntry) Append(text string) {
	e.RawText += text
	e.Invalidate()
}

func (e *ChatEntry) render(width int) string {
	switch e.Role {
	case "assistant":
		text := e.RawText
		if text == "" {
			return ""
		}
		return trimLeadingRenderedSpacing(RenderMarkdownWidth(text, max(20, width-2)))
	case "user":
		lines := wrapConversationText(strings.TrimRight(e.RawText, "\n"), max(1, width))
		return strings.Join(lines, "\n")
	case "system":
		return e.RawText // already styled by caller
	case "tool":
		return e.RawText // already formatted
	case "compaction":
		return e.RawText
	default:
		return e.RawText
	}
}

// ChatEntryList is a thread-safe list of ChatEntry items.
type ChatEntryList struct {
	mu      sync.Mutex
	entries []ChatEntry
	dirty   bool
}

// NewChatEntryList creates a new empty list.
func NewChatEntryList() *ChatEntryList {
	return &ChatEntryList{}
}

// Append adds an entry.
func (l *ChatEntryList) Append(e ChatEntry) {
	l.mu.Lock()
	l.entries = append(l.entries, e)
	l.dirty = true
	l.mu.Unlock()
}

// InvalidateAll clears all render caches.
func (l *ChatEntryList) InvalidateAll() {
	l.mu.Lock()
	for i := range l.entries {
		l.entries[i].Invalidate()
	}
	l.dirty = true
	l.mu.Unlock()
}

// Render produces the full conversation content string for a given width.
func (l *ChatEntryList) Render(width int) string {
	l.mu.Lock()
	defer l.mu.Unlock()

	var sb strings.Builder
	for i := range l.entries {
		rendered := l.entries[i].Rendered(width)
		if rendered == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(rendered)
	}
	l.dirty = false
	return sb.String()
}

// Last returns a pointer to the last entry, or nil if empty.
func (l *ChatEntryList) Last() *ChatEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return nil
	}
	return &l.entries[len(l.entries)-1]
}

// LastMatching returns the last entry with the given role.
func (l *ChatEntryList) LastMatching(role string) *ChatEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.entries) - 1; i >= 0; i-- {
		if l.entries[i].Role == role {
			return &l.entries[i]
		}
	}
	return nil
}

// Len returns the number of entries.
func (l *ChatEntryList) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// Reset clears all entries.
func (l *ChatEntryList) Reset() {
	l.mu.Lock()
	l.entries = nil
	l.dirty = false
	l.mu.Unlock()
}

// LineCount estimates the number of visual lines at the given width.
func (l *ChatEntryList) LineCount(width int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for i := range l.entries {
		r := l.entries[i].Rendered(width)
		if r == "" {
			continue
		}
		count += strings.Count(r, "\n") + 1
	}
	return count
}

// TrimOldest removes the oldest entries until the count is at most max.
func (l *ChatEntryList) TrimOldest(max int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) <= max {
		return
	}
	copy(l.entries, l.entries[len(l.entries)-max:])
	l.entries = l.entries[:max]
	l.dirty = true
}
