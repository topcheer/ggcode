package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// homeWarnModel is a full-screen Bubble Tea program that warns the user
// they are running ggcode from their HOME directory.
type homeWarnModel struct {
	lang     Language
	selected int // 0 = continue, 1 = exit
	quitting bool
}

func newHomeWarnModel(lang Language) homeWarnModel {
	return homeWarnModel{lang: lang}
}

func (m *homeWarnModel) Init() tea.Cmd {
	return nil
}

func (m *homeWarnModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.selected < 1 {
				m.selected++
			}
		case "enter":
			m.quitting = true
			return m, tea.Quit
		case "c":
			m.selected = 0
			m.quitting = true
			return m, tea.Quit
		case "e", "q", "esc":
			m.selected = 1
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *homeWarnModel) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	title := tr(m.lang, "home.warn.title")
	message := tr(m.lang, "home.warn.message")
	continueText := tr(m.lang, "home.warn.continue")
	exitText := tr(m.lang, "home.warn.exit")
	shortcutC := tr(m.lang, "home.warn.shortcut_c")
	shortcutE := tr(m.lang, "home.warn.shortcut_e")

	// Styles
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFAA00")).
		MarginBottom(1)

	messageStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#CCCCCC")).
		MarginBottom(2)

	selectedStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00FF00"))

	unselectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888"))

	continueLine := fmt.Sprintf("  [%s] %s", shortcutC, continueText)
	exitLine := fmt.Sprintf("  [%s] %s", shortcutE, exitText)

	if m.selected == 0 {
		continueLine = selectedStyle.Render("▸ " + continueLine)
		exitLine = unselectedStyle.Render("  " + exitLine)
	} else {
		continueLine = unselectedStyle.Render("  " + continueLine)
		exitLine = selectedStyle.Render("▸ " + exitLine)
	}

	// Measure terminal width (use 60 as default for styling)
	boxStyle := lipgloss.NewStyle().
		Width(60).
		Padding(2, 4).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#FFAA00"))

	content := titleStyle.Render(title) + "\n" +
		messageStyle.Render(message) + "\n" +
		continueLine + "\n" +
		exitLine

	return tea.NewView(lipgloss.NewStyle().
		Padding(4, 0).
		Render(boxStyle.Render(content)))
}

// ShouldContinue returns true if the user chose to continue, false if exit.
func (m *homeWarnModel) ShouldContinue() bool {
	return m.selected == 0
}

// ConfirmHomeDir shows a full-screen TUI warning that the user is running
// from their HOME directory. Returns true if the user wants to continue,
// false if they want to exit.
func ConfirmHomeDir(lang Language) bool {
	m := newHomeWarnModel(lang)
	p := tea.NewProgram(&m)
	model, err := p.Run()
	if err != nil {
		// If the TUI fails (e.g. no tty), default to continue
		return true
	}
	return model.(*homeWarnModel).ShouldContinue()
}

// NormalizeLanguage converts a language string to a Language value.
// Exported for use by cmd/ggcode.
func NormalizeLanguage(s string) Language {
	return normalizeLanguage(s)
}
