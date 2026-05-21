package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/im"
)

// qrOverlayState holds the state for the QR code overlay panel.
type qrOverlayState struct {
	title       string // e.g. "Telegram — Scan to Add Bot"
	description string // scan hint text
	qrText      string // rendered QR code ASCII art
	footer      string // URI or additional info line
}

// openQROverlayFromStates creates a QR overlay from adapter states (contact URI).
func (m *Model) openQROverlayFromStates(platformName string, states []*im.AdapterState) bool {
	var contactURI string
	for _, s := range states {
		if s != nil && s.ContactURI != "" {
			contactURI = s.ContactURI
			break
		}
	}
	if contactURI == "" {
		return false
	}

	qrText := renderContactQRCode(contactURI)
	m.qrOverlay = &qrOverlayState{
		title:       fmt.Sprintf("%s — %s", m.t("panel.qr.title"), platformName),
		description: m.t("panel.qr.scan_hint"),
		qrText:      qrText,
		footer:      contactURI,
	}
	return true
}

// openQROverlayDirect opens a QR overlay with explicit content.
func (m *Model) openQROverlayDirect(title, description, qrText, footer string) {
	m.qrOverlay = &qrOverlayState{
		title:       title,
		description: description,
		qrText:      qrText,
		footer:      footer,
	}
}

// closeQROverlay closes the QR overlay panel.
func (m *Model) closeQROverlay() {
	m.qrOverlay = nil
}

// renderQROverlay renders the full QR overlay panel.
func (m Model) renderQROverlay() string {
	if m.qrOverlay == nil {
		return ""
	}
	o := m.qrOverlay

	var body []string

	// Title
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")).Render(
		fmt.Sprintf(" %s", o.title),
	)
	body = append(body, title, "")

	// Description
	if o.description != "" {
		hint := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
			fmt.Sprintf(" %s", o.description),
		)
		body = append(body, hint, "")
	}

	// QR code (indented for visual padding)
	if o.qrText != "" {
		indentedQR := indentLines(o.qrText, 2)
		body = append(body, indentedQR, "")
	}

	// Footer (URI or extra info)
	if o.footer != "" {
		footerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		body = append(body, footerStyle.Render(fmt.Sprintf(" %s", o.footer)), "")
	}

	// Mobile app download links
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	body = append(body,
		dimStyle.Render(" Get GGCode Mobile:"),
		dimStyle.Render("   iOS:     https://testflight.apple.com/join/J34wVD6p"),
		dimStyle.Render("   Android: https://play.google.com/apps/testing/gg.ai.ggcode.mobile"),
		"",
	)

	// Esc hint
	escHint := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		" " + m.t("panel.qr.esc_hint"),
	)
	body = append(body, escHint)

	return m.renderContextBox("/qr", strings.Join(body, "\n"), lipgloss.Color("12"))
}

// handleQROverlayKey handles key presses in the QR overlay.
func (m *Model) handleQROverlayKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.qrOverlay = nil
	}
	return *m, nil
}

// indentLines prefixes each line with n spaces for visual padding.
func indentLines(s string, n int) string {
	pad := strings.Repeat(" ", n)
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(pad)
		b.WriteString(line)
	}
	return b.String()
}
