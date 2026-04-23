package chat

import (
	"fmt"
	"strings"
)

// UserItem renders a user message with a prefix icon.
type UserItem struct {
	CachedItem
	id     string
	text   string
	prefix string
	styles Styles
}

// NewUserItem creates a new user message item.
func NewUserItem(id, text string, styles Styles) *UserItem {
	return &UserItem{
		id:     id,
		text:   text,
		prefix: styles.UserPrefix,
		styles: styles,
	}
}

func (u *UserItem) ID() string { return u.id }

func (u *UserItem) Render(width int) string {
	if cached, h, ok := u.GetCached(width); ok {
		_ = h
		return cached
	}

	// Render: prefix + text (word-wrapped to width)
	prefixWidth := len(u.prefix)
	contentWidth := width - prefixWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	lines := wrapLines(u.text, contentWidth)
	var sb strings.Builder
	for i, line := range lines {
		if i == 0 {
			sb.WriteString(u.prefix)
		} else {
			sb.WriteString(strings.Repeat(" ", prefixWidth))
		}
		sb.WriteString(line)
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
	}

	rendered := sb.String()
	u.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

func (u *UserItem) Height(width int) int {
	if _, h, ok := u.GetCached(width); ok {
		return h
	}
	return measureHeight(u.Render(width))
}

// wrapLines does simple word wrapping at the given width.
func wrapLines(text string, width int) []string {
	var result []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			result = append(result, "")
			continue
		}
		// Simple character-based wrapping
		for len(paragraph) > 0 {
			if len(paragraph) <= width {
				result = append(result, paragraph)
				break
			}
			// Find last space before width
			cut := width
			spaceIdx := strings.LastIndex(paragraph[:cut], " ")
			if spaceIdx > 0 {
				cut = spaceIdx
			}
			result = append(result, paragraph[:cut])
			paragraph = strings.TrimLeft(paragraph[cut:], " ")
		}
	}
	if len(result) == 0 {
		result = []string{""}
	}
	return result
}

// --- Assistant Item ---

// AssistantItem renders an assistant message (supports streaming).
type AssistantItem struct {
	CachedItem
	id        string
	text      string
	prefix    string
	styles    Styles
	streaming bool
}

// NewAssistantItem creates a new assistant message item.
func NewAssistantItem(id string, styles Styles) *AssistantItem {
	return &AssistantItem{
		id:        id,
		prefix:    styles.AssistantPrefix,
		styles:    styles,
		streaming: true,
	}
}

func (a *AssistantItem) ID() string { return a.id }

// SetText updates the assistant content (for streaming).
func (a *AssistantItem) SetText(text string) {
	if a.text != text {
		a.text = text
		a.Invalidate()
	}
}

// SetFinished marks the assistant as done streaming.
func (a *AssistantItem) SetFinished() {
	a.streaming = false
}

func (a *AssistantItem) Render(width int) string {
	if cached, _, ok := a.GetCached(width); ok && !a.streaming {
		return cached
	}

	prefixWidth := len(a.prefix)
	contentWidth := width - prefixWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	lines := wrapLines(a.text, contentWidth)
	var sb strings.Builder
	for i, line := range lines {
		if i == 0 {
			sb.WriteString(a.prefix)
		} else {
			sb.WriteString(strings.Repeat(" ", prefixWidth))
		}
		sb.WriteString(line)
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
	}

	rendered := sb.String()
	if !a.streaming {
		a.SetCached(rendered, width, measureHeight(rendered))
	}
	return rendered
}

func (a *AssistantItem) Height(width int) int {
	if _, h, ok := a.GetCached(width); ok && !a.streaming {
		return h
	}
	return measureHeight(a.Render(width))
}

// --- System Item ---

// SystemItem renders a system/status/info message.
type SystemItem struct {
	CachedItem
	id     string
	text   string
	styles Styles
}

// NewSystemItem creates a new system message item.
func NewSystemItem(id, text string, styles Styles) *SystemItem {
	return &SystemItem{
		id:     id,
		text:   text,
		styles: styles,
	}
}

func (s *SystemItem) ID() string { return s.id }

func (s *SystemItem) Render(width int) string {
	if cached, _, ok := s.GetCached(width); ok {
		return cached
	}
	rendered := s.styles.SystemStyle.Render(fmt.Sprintf("%s%s", s.styles.SystemPrefix, s.text))
	s.SetCached(rendered, width, measureHeight(rendered))
	return rendered
}

func (s *SystemItem) Height(width int) int {
	if _, h, ok := s.GetCached(width); ok {
		return h
	}
	return measureHeight(s.Render(width))
}

// --- Spacer Item ---

// SpacerItem adds vertical space between message groups.
type SpacerItem struct {
	height int
}

// NewSpacerItem creates a spacer with the given height in lines.
func NewSpacerItem(height int) *SpacerItem {
	return &SpacerItem{height: height}
}

func (s *SpacerItem) ID() string { return "" }

func (s *SpacerItem) Render(width int) string {
	return strings.Repeat("\n", max(s.height-1, 0))
}

func (s *SpacerItem) Height(width int) int {
	return s.height
}
