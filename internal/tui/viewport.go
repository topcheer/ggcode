package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ViewportModel wraps a viewport with auto-follow behavior.
type ViewportModel struct {
	vp        viewport.Model
	autoFollow bool
	width     int
	height    int
}

// NewViewportModel creates a new viewport model.
func NewViewportModel(width, height int) ViewportModel {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return ViewportModel{
		vp:        vp,
		autoFollow: true,
		width:     width,
		height:    height,
	}
}

// SetContent sets the viewport content and auto-scrolls to bottom if following.
func (v *ViewportModel) SetContent(content string) {
	v.vp.SetContent(content)
	if v.autoFollow {
		v.vp.GotoBottom()
	}
}

// Content returns the current viewport content.
func (v *ViewportModel) Content() string {
	return v.vp.View()
}

// GotoBottom scrolls to the bottom and enables auto-follow.
func (v *ViewportModel) GotoBottom() {
	v.autoFollow = true
	v.vp.GotoBottom()
}

// SetSize updates the viewport dimensions.
func (v *ViewportModel) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.vp.Width = width
	v.vp.Height = height
}

// AutoFollow returns whether auto-follow is enabled.
func (v *ViewportModel) AutoFollow() bool {
	return v.autoFollow
}

// ScrollUp scrolls up by n lines.
func (v *ViewportModel) ScrollUp(n int) {
	v.autoFollow = false
	v.vp.ScrollUp(n)
}

// ScrollDown scrolls down by n lines.
func (v *ViewportModel) ScrollDown(n int) {
	v.vp.ScrollDown(n)
	// Check if we've reached the bottom
	if v.vp.AtBottom() {
		v.autoFollow = true
	}
}

// AtBottom returns true if the viewport is at the bottom.
func (v *ViewportModel) AtBottom() bool {
	return v.vp.AtBottom()
}

// TotalLineCount returns the total number of content lines.
func (v *ViewportModel) TotalLineCount() int {
	return v.vp.TotalLineCount()
}

// VisibleLineCount returns the number of visible lines.
func (v *ViewportModel) VisibleLineCount() int {
	return v.vp.Height
}

// YOffset returns the current vertical scroll offset.
func (v *ViewportModel) YOffset() int {
	return v.vp.YOffset
}

// Update handles messages for the viewport.
func (v ViewportModel) Update(msg tea.Msg) (ViewportModel, tea.Cmd) {
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

// View renders the viewport.
func (v ViewportModel) View() string {
	return v.vp.View()
}

// ScrollIndicatorStyle returns a styled scroll indicator.
func (v ViewportModel) ScrollIndicatorStyle() string {
	if v.vp.AtBottom() && v.autoFollow {
		return ""
	}
	total := v.vp.TotalLineCount()
	if total == 0 {
		return ""
	}
	offset := v.vp.YOffset
	visible := v.vp.Height
	pct := float64(offset+visible) / float64(total) * 100
	if pct > 100 {
		pct = 100
	}
	indicator := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render
	return indicator("▼ " + strings.TrimSpace(scrollBar(offset, visible, total, 20)))
}

// scrollBar creates a simple text scroll bar.
func scrollBar(offset, visible, total, width int) string {
	if total <= visible {
		return strings.Repeat("█", width)
	}
	barLen := float64(visible) / float64(total) * float64(width)
	pos := float64(offset) / float64(total-visible) * float64(width) - barLen
	if pos < 0 {
		pos = 0
	}
	bar := strings.Repeat("░", width)
	start := int(pos)
	end := int(pos + barLen)
	if end > width {
		end = width
	}
	if start < end {
		runes := []rune(bar)
		for i := start; i < end; i++ {
			runes[i] = '█'
		}
		bar = string(runes)
	}
	return bar
}
