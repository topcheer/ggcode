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
	platformName string // e.g. "Telegram", "Discord"
	contactURI   string // deep link URL
	qrText       string // rendered QR code ASCII art
	returnPanel  string // which panel to return to (for key routing)
}

// openQROverlay creates a QR overlay for the first adapter with a ContactURI.
// entries is a slice of structs that have an AdapterState *im.AdapterState field.
func (m *Model) openQROverlay(platformName string, entries interface{ GetAdapterState() *im.AdapterState }) bool {
	// Use reflection-free approach: just pass the entries directly
	return false
}

// openQROverlayFromStates creates a QR overlay from adapter states.
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
		platformName: platformName,
		contactURI:   contactURI,
		qrText:       qrText,
	}
	return true
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
		fmt.Sprintf(" %s — %s", m.t("panel.qr.title"), o.platformName),
	)
	body = append(body, title, "")

	// Scan hint
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		" " + m.t("panel.qr.scan_hint"),
	)
	body = append(body, hint, "")

	// QR code
	if o.qrText != "" {
		body = append(body, o.qrText, "")
	}

	// Contact URI
	uriStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	body = append(body, uriStyle.Render(fmt.Sprintf(" %s", o.contactURI)), "")

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
