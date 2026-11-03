package tui

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type ViewportModel struct {
	vp         viewport.Model
	autoFollow bool
	width      int
	height     int
}

func NewViewportModel(width, height int) ViewportModel {
	vp := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	vp.SetContent("")
	return ViewportModel{
		vp:         vp,
		autoFollow: true,
		width:      width,
		height:     height,
	}
}

func (v *ViewportModel) SetContent(content string) {
	v.vp.SetContent(content)
	if v.autoFollow {
		v.vp.GotoBottom()
	}
}

func (v *ViewportModel) Content() string {
	return v.vp.View()
}

func (v *ViewportModel) GotoBottom() {
	v.autoFollow = true
	v.vp.GotoBottom()
}

func (v *ViewportModel) SetSize(width, height int) {
	v.width = width
	v.height = height
	v.vp.SetWidth(width)
	v.vp.SetHeight(height)

	if v.autoFollow {
		v.vp.GotoBottom()
	}
}

func (v *ViewportModel) AutoFollow() bool {
	return v.autoFollow
}

func (v *ViewportModel) ScrollUp(n int) {
	v.autoFollow = false
	v.vp.ScrollUp(n)
}

func (v *ViewportModel) ScrollDown(n int) {
	v.vp.ScrollDown(n)

	if v.vp.AtBottom() {
		v.autoFollow = true
	}
}

func (v *ViewportModel) AtBottom() bool {
	return v.vp.AtBottom()
}

func (v *ViewportModel) TotalLineCount() int {
	return v.vp.TotalLineCount()
}

func (v *ViewportModel) VisibleLineCount() int {
	return v.vp.Height()
}

func (v *ViewportModel) YOffset() int {
	return v.vp.YOffset()
}

func (v ViewportModel) Update(msg tea.Msg) (ViewportModel, tea.Cmd) {
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return v, cmd
}

func (v ViewportModel) View() tea.View {
	return tea.NewView(v.vp.View())
}

func (v ViewportModel) ScrollIndicatorStyle() string {
	if v.vp.AtBottom() && v.autoFollow {
		return ""
	}
	total := v.vp.TotalLineCount()
	if total == 0 {
		return ""
	}
	offset := v.vp.YOffset()
	visible := v.vp.Height()
	pct := float64(offset+visible) / float64(total) * 100
	if pct > 100 {
		pct = 100
	}
	indicator := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render
	return indicator("▼ " + strings.TrimSpace(scrollBar(offset, visible, total, 20)))
}

func scrollBar(offset, visible, total, width int) string {
	if total <= visible {
		return strings.Repeat("█", width)
	}
	barLen := float64(visible) / float64(total) * float64(width)
	pos := float64(offset)/float64(total-visible)*float64(width) - barLen
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
