package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// FullscreenModel wraps the main Model with alt-screen and viewport support.
type FullscreenModel struct {
	inner      Model // the original TUI model
	viewport   ViewportModel
	fullscreen bool
}

// NewFullscreenModel creates a fullscreen model wrapping the inner model.
func NewFullscreenModel(inner Model) FullscreenModel {
	return FullscreenModel{
		inner:      inner,
		viewport:   NewViewportModel(inner.width, inner.height),
		fullscreen: true,
	}
}

// ToggleFullscreen toggles fullscreen mode.
func (f *FullscreenModel) ToggleFullscreen() {
	f.fullscreen = !f.fullscreen
	if f.fullscreen {
		f.viewport.GotoBottom()
	}
}

// Fullscreen returns whether fullscreen mode is active.
func (f *FullscreenModel) Fullscreen() bool {
	return f.fullscreen
}

// Inner returns a pointer to the inner model.
func (f *FullscreenModel) Inner() *Model {
	return &f.inner
}

// syncViewport copies the inner model's output to the viewport.
func (f *FullscreenModel) syncViewport() {
	content := f.inner.renderOutput()
	f.viewport.SetContent(content)
}

// Init initializes the fullscreen model.
func (f FullscreenModel) Init() tea.Cmd {
	return f.inner.Init()
}

// Update handles messages for the fullscreen model.
func (f FullscreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		f.inner.width = msg.Width
		f.inner.height = msg.Height
		f.viewport.SetSize(msg.Width, msg.Height-4) // Reserve space for input + status bar
		return f, nil

	case tea.MouseMsg:
		switch msg.Type {
		case tea.MouseWheelUp:
			if f.fullscreen {
				f.viewport.ScrollUp(3)
				return f, nil
			}
		case tea.MouseWheelDown:
			if f.fullscreen {
				f.viewport.ScrollDown(3)
				return f, nil
			}
		}
		return f, nil

	case tea.KeyMsg:
		if !f.inner.loading && f.inner.pendingApproval == nil {
			// Fullscreen toggle: Ctrl+L or /fullscreen command handled in inner
		}
	}

	// Delegate to inner model
	inner, cmd := f.inner.Update(msg)
	f.inner = inner.(Model)

	// Sync viewport content
	f.syncViewport()

	return f, cmd
}

// View renders the fullscreen UI.
func (f FullscreenModel) View() string {
	if f.inner.quitting {
		return ""
	}

	if !f.fullscreen {
		// Fallback to non-fullscreen rendering
		return f.inner.View()
	}

	// Fullscreen layout with viewport
	title := f.inner.styles.title.Render("ggcode")
	input := f.inner.input.View()

	var sb strings.Builder

	// Title bar
	sb.WriteString(title)
	sb.WriteString("\n")

	// Viewport with content
	sb.WriteString(f.viewport.View())
	sb.WriteString("\n")

	// Status bar
	statusBar := f.renderStatusBar()
	sb.WriteString(statusBar)
	sb.WriteString("\n")

	// Input line
	if f.inner.loading && f.inner.spinner.IsActive() {
		sb.WriteString(f.inner.spinner.String())
	} else if f.inner.loading {
		sb.WriteString("▌")
	}
	sb.WriteString(input)

	// Mode hint
	if !f.inner.loading && f.inner.pendingApproval == nil {
		modeStr := f.inner.styles.prompt.Render(
			"[" + f.inner.mode.String() + "] Ctrl+L | /fullscreen | /?",
		)
		sb.WriteString(" " + modeStr)
	}

	return sb.String()
}

// renderStatusBar shows a compact status line.
func (f FullscreenModel) renderStatusBar() string {
	s := f.inner.styles.prompt

	var parts []string
	parts = append(parts, "mode "+f.inner.renderModeBadge())
	if f.inner.lastCost != "" {
		parts = append(parts, f.inner.lastCost)
	}
	if f.inner.session != nil {
		parts = append(parts, f.inner.session.ID)
	}

	scrollInfo := f.viewport.ScrollIndicatorStyle()
	if scrollInfo != "" {
		parts = append(parts, scrollInfo)
	}

	if len(parts) == 0 {
		return ""
	}

	return s.Render(strings.Join(parts, " │ "))
}

// RenderContent renders the inner model output content (for viewport).
// This is exposed so the inner model can be used independently.
func (f *FullscreenModel) RenderContent() string {
	return f.inner.renderOutput()
}
