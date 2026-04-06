package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	logoGGStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	logoCODEStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	logoTaglineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Faint(true)
	logoSubtitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func renderLogo(width int) string {
	if strings.TrimSpace(os.Getenv("GGCODE_ASCII_LOGO")) != "" {
		return asciiLogo()
	}
	return renderWordmark(width)
}

func renderWordmark(width int) string {
	if width < 26 {
		name := logoGGStyle.Render("GG") + logoCODEStyle.Render("CODE")
		return name
	}
	gg := strings.Join([]string{
		"┏━┓ ┏━┓",
		"┃╺┓ ┃╺┓",
		"┗━┛ ┗━┛",
	}, "\n")
	code := strings.Join([]string{
		"┏━┓┏━┓┏━┓┏━┓",
		"┃  ┃ ┃┃ ┃┣╸ ",
		"┗━┛┗━┛┗━┛┗━┛",
	}, "\n")
	name := lipgloss.JoinHorizontal(
		lipgloss.Top,
		logoGGStyle.Render(gg),
		"  ",
		logoCODEStyle.Render(code),
	)
	return lipgloss.JoinVertical(lipgloss.Left,
		name,
		logoTaglineStyle.Render("AI CODING CLI TOOL"),
	)
}

func renderHeaderLogo(width int, subtitle string) string {
	logo := renderLogo(width)
	if logo == asciiLogo() {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(logo) +
			logoSubtitleStyle.Render(subtitle)
	}
	if strings.TrimSpace(subtitle) == "" {
		return logo
	}
	return lipgloss.JoinVertical(lipgloss.Left, logo, logoSubtitleStyle.Render(subtitle))
}

func renderSidebarLogo(width int, subtitle string) string {
	logo := renderLogo(width)
	if logo == asciiLogo() {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(logo) +
			logoSubtitleStyle.Render(subtitle)
	}
	if strings.TrimSpace(subtitle) == "" {
		return logo
	}
	return lipgloss.JoinVertical(lipgloss.Left, logo, logoSubtitleStyle.Render(subtitle))
}
