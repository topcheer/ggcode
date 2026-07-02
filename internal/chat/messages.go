package chat

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/markdown"
)

// UserItem renders a user message with a prefix icon.
type UserItem struct {
	CachedItem
	id       string
	text     string
	prefix   string
	styles   Styles
	markdown bool // render text as markdown instead of plain text
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

// NewMarkdownUserItem creates a user message item that renders its content as
// markdown. Used for messages that may contain structured text (e.g. LAN Chat
// agent-to-agent messages with markdown formatting).
func NewMarkdownUserItem(id, text string, styles Styles) *UserItem {
	return &UserItem{
		id:       id,
		text:     text,
		prefix:   styles.UserPrefix,
		styles:   styles,
		markdown: true,
	}
}

func (u *UserItem) ID() string { return u.id }

func (u *UserItem) Text() string { return u.text }

func (u *UserItem) Prefix() string { return u.prefix }

func (u *UserItem) SetPrefix(prefix string) {
	u.prefix = prefix
	u.Invalidate()
}

func (u *UserItem) Render(width int) string {
	if cached, h, ok := u.GetCached(width); ok {
		_ = h
		return cached
	}

	prefixWidth := lipgloss.Width(u.styles.UserStyle.Render(u.prefix))
	contentWidth := width - prefixWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	var content string
	if u.markdown {
		content = markdown.Render(u.text, contentWidth)
	} else {
		content = strings.Join(wrapLines(u.text, contentWidth), "\n")
	}

	lines := strings.Split(content, "\n")
	var sb strings.Builder
	for i, line := range lines {
		if i == 0 {
			sb.WriteString(u.styles.UserStyle.Render(u.prefix))
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
		// Visual-width-aware wrapping
		for paragraph != "" {
			if lipgloss.Width(paragraph) <= width {
				result = append(result, paragraph)
				break
			}
			// Walk runes forward to find the longest prefix that fits
			runes := []rune(paragraph)
			cut := 0
			for cut < len(runes) && lipgloss.Width(string(runes[:cut+1])) <= width {
				cut++
			}
			if cut == 0 {
				// Single wide character wider than width — emit it anyway
				cut = 1
			}
			// Try to break at a space for cleaner wrapping
			chunk := string(runes[:cut])
			if spaceIdx := strings.LastIndex(chunk, " "); spaceIdx > 0 {
				// Convert byte index to rune index for safe slicing
				runeIdx := utf8.RuneCountInString(chunk[:spaceIdx])
				chunk = string(runes[:runeIdx])
				cut = runeIdx
			}
			result = append(result, chunk)
			paragraph = strings.TrimLeft(string(runes[cut:]), " ")
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
	id             string
	text           string
	reasoning      string // accumulated thinking/reasoning content
	reasoningOk    bool   // true once reasoning is complete (collapsed)
	prefix         string
	styles         Styles
	streaming      bool
	textCache      streamRenderCache
	reasoningCache streamRenderCache
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

func (a *AssistantItem) Text() string { return a.text }

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
	a.textCache = streamRenderCache{}
	a.reasoningCache = streamRenderCache{}
	a.Invalidate()
}

// Reasoning returns the current reasoning content.
func (a *AssistantItem) Reasoning() string {
	return a.reasoning
}

// SetReasoning updates the reasoning/thinking content.
func (a *AssistantItem) SetReasoning(text string) {
	if a.reasoning != text {
		a.reasoning = text
		a.reasoningOk = false
		a.Invalidate()
	}
}

// SetReasoningFinished collapses reasoning into a one-line summary.
func (a *AssistantItem) SetReasoningFinished() {
	a.reasoningOk = true
	a.reasoningCache = streamRenderCache{}
	a.Invalidate()
}

func (a *AssistantItem) Render(width int) string {
	if cached, _, ok := a.GetCached(width); ok {
		return cached
	}

	prefixWidth := lipgloss.Width(a.styles.AssistantStyle.Render(a.prefix))
	contentWidth := width - prefixWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	var result string

	// Render reasoning block if present.
	if a.reasoning != "" {
		result = a.renderReasoning(width, prefixWidth, contentWidth)
	}

	// Render main assistant content — but only if there is actual text content.
	// When a turn has reasoning but no body text (e.g. only tool calls),
	// rendering the empty prefix line is visually noise.
	if a.text != "" || (a.reasoning == "" && !a.streaming) {
		rendered := a.renderMainContent(contentWidth)
		lines := strings.Split(rendered, "\n")
		var sb strings.Builder
		for i, line := range lines {
			if i == 0 {
				sb.WriteString(a.styles.AssistantStyle.Render(a.prefix))
			} else {
				sb.WriteString(strings.Repeat(" ", prefixWidth))
			}
			sb.WriteString(line)
			if i < len(lines)-1 {
				sb.WriteString("\n")
			}
		}
		if result != "" {
			result += "\n\n" + sb.String()
		} else {
			result = sb.String()
		}
	}

	a.SetCached(result, width, measureHeight(result))
	return result
}

func (a *AssistantItem) renderMainContent(contentWidth int) string {
	if a.streaming {
		rendered, cache := renderStreamingMarkdown(a.text, contentWidth, &a.textCache)
		a.textCache = cache
		return rendered
	}
	a.textCache = streamRenderCache{}
	return markdown.Render(a.text, contentWidth)
}

func (a *AssistantItem) renderReasoning(width, prefixWidth, contentWidth int) string {
	// Always render full reasoning text — no collapsing (TUI can't expand).
	reasoningRendered := a.reasoning
	if !a.reasoningOk {
		rendered, cache := renderStreamingMarkdown(a.reasoning, contentWidth, &a.reasoningCache)
		a.reasoningCache = cache
		reasoningRendered = rendered
	} else {
		a.reasoningCache = streamRenderCache{}
		reasoningRendered = markdown.Render(a.reasoning, contentWidth)
	}
	lines := strings.Split(reasoningRendered, "\n")
	var sb strings.Builder
	for i, line := range lines {
		if i == 0 {
			sb.WriteString(a.styles.ReasoningPrefix)
		} else {
			sb.WriteString(strings.Repeat(" ", prefixWidth))
		}
		sb.WriteString(a.styles.ReasoningStyle.Render(line))
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (a *AssistantItem) Height(width int) int {
	if _, h, ok := a.GetCached(width); ok {
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

func (s *SystemItem) Text() string { return s.text }

func (s *SystemItem) SetText(text string) {
	s.text = text
	s.Invalidate()
}

func (s *SystemItem) AppendText(text string) {
	s.text += text
	s.Invalidate()
}

func (s *SystemItem) Render(width int) string {
	if cached, _, ok := s.GetCached(width); ok {
		return cached
	}

	prefix := s.styles.SystemPrefix
	prefixWidth := lipgloss.Width(prefix)
	contentWidth := width - prefixWidth
	if contentWidth < 10 {
		contentWidth = 10
	}

	// Wrap each paragraph to the available content width so that lines
	// (including the leading prefix/indent) never exceed the viewport.
	// Without this, long system messages overflow the terminal width,
	// causing invisible auto-wrap lines that measureHeight() doesn't count
	// and the virtual list renders too many items.
	textLines := wrapLines(s.text, contentWidth)
	var sb strings.Builder
	for i, line := range textLines {
		if i == 0 {
			sb.WriteString(s.styles.SystemStyle.Render(prefix))
		} else {
			sb.WriteString(strings.Repeat(" ", prefixWidth))
		}
		sb.WriteString(s.styles.SystemStyle.Render(line))
		if i < len(textLines)-1 {
			sb.WriteString("\n")
		}
	}

	rendered := sb.String()
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
