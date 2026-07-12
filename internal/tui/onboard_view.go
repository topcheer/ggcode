package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *onboardModel) View() tea.View {
	if m.width == 0 {
		return tea.NewView("")
	}

	var content string
	switch m.step {
	case onboardStepLanguage:
		content = m.viewLanguage()
	case onboardStepVendor:
		content = m.viewVendor()
	case onboardStepCustom:
		content = m.viewCustom()
	case onboardStepEndpoint:
		content = m.viewEndpoint()
	case onboardStepModel:
		content = m.viewModel()
	case onboardStepOptional:
		content = m.viewOptional()
	case onboardStepIM:
		content = m.viewIM()
	case onboardStepDone:
		return tea.NewView("")
	}

	header := lipgloss.NewStyle().Bold(true).Render(m.tr("title"))
	stepLabel := fmt.Sprintf("[%d/6]", int(m.step)+1)

	top := lipgloss.NewStyle().
		Width(m.width).
		Padding(1, 0).
		Render(header + "  " + stepLabel)

	bottom := ""
	if m.err != "" {
		bottom = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(m.err)
	}
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Render(m.tr("hint_nav"))

	return tea.NewView(lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Padding(2, 4).
		Render(top + "\n\n" + content + "\n\n" + bottom + "\n" + hint))
}

func (m *onboardModel) viewLanguage() string {
	var b strings.Builder
	b.WriteString(m.tr("step_language") + ":\n\n")
	for i, lang := range m.langs {
		cursor := "  "
		if i == m.langCursor {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", cursor, lang.name))
	}
	return b.String()
}

func (m *onboardModel) viewVendor() string {
	var b strings.Builder
	b.WriteString(m.tr("step_vendor") + ":\n\n")
	if m.vendorFilter.Value() != "" || m.vendorFilter.Focused() {
		b.WriteString(fmt.Sprintf("%s %s\n\n", m.tr("filter"), m.vendorFilter.View()))
	}
	for i, idx := range m.vendorFiltered {
		p := m.presets[idx]
		cursor := "  "
		if i == m.vendorCursor {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", cursor, p.DisplayName))
	}
	if len(m.vendorFiltered) == 0 {
		b.WriteString("  No matching vendors.\n")
	}
	// Custom provider entry (always shown)
	{
		cursor := "  "
		if m.vendorCursor == len(m.vendorFiltered) {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", cursor, m.tr("custom_vendor")))
	}
	b.WriteString("\n  " + m.tr("hint_filter"))
	return b.String()
}

func (m *onboardModel) viewEndpoint() string {
	var b strings.Builder
	ep := m.selectedVendor.Endpoints
	b.WriteString(m.trf("step_endpoint", m.selectedVendor.DisplayName) + ":\n\n")

	epFocusMark := "  "
	if m.epFocus == focusEndpoint {
		epFocusMark = "▸ "
	}
	selectedEP := ep[m.endpointCursor]
	b.WriteString(fmt.Sprintf("%s%s %s", epFocusMark, m.tr("endpoint_label"), selectedEP.DisplayName))
	if m.epFocus == focusEndpoint {
		b.WriteString("\n")
		for i, e := range ep {
			cursor := "    "
			style := e.DisplayName
			if i == m.endpointCursor {
				cursor = "  > "
				style = lipgloss.NewStyle().Bold(true).Render(e.DisplayName)
			}
			b.WriteString(fmt.Sprintf("%s%s\n", cursor, style))
		}
	} else {
		b.WriteString("  (Tab)\n")
	}

	b.WriteString("\n")

	keyFocusMark := "  "
	if m.epFocus == focusAPIKey {
		keyFocusMark = "▸ "
	}
	b.WriteString(keyFocusMark + m.tr("apikey_label") + "\n")
	b.WriteString("  " + m.apiKeyInput.View())
	if m.epFocus == focusAPIKey {
		b.WriteString("  Enter")
	}
	b.WriteString("\n")
	return b.String()
}

func (m *onboardModel) viewModel() string {
	var b strings.Builder
	// For custom providers, use the custom name instead of endpoint display name
	if m.selectedVendor.ID == "" && m.customResolved != nil {
		b.WriteString(m.trf("step_model", m.customResolved.EndpointName) + ":\n\n")
	} else {
		ep := m.selectedVendor.Endpoints[m.endpointCursor]
		b.WriteString(m.trf("step_model", ep.DisplayName) + ":\n\n")
	}

	if m.modelLoading {
		b.WriteString("  " + m.tr("loading_models") + "\n\n")
	}

	if m.modelFilter.Value() != "" || m.modelFilter.Focused() {
		b.WriteString(fmt.Sprintf("%s %s\n\n", m.tr("filter"), m.modelFilter.View()))
	}

	displayed := 0
	for i, idx := range m.modelFiltered {
		if displayed >= 20 {
			b.WriteString(fmt.Sprintf("\n  ... +%d", len(m.modelFiltered)-displayed))
			break
		}
		cursor := "  "
		if i == m.modelCursor {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", cursor, m.models[idx]))
		displayed++
	}

	if len(m.modelFiltered) == 0 {
		b.WriteString("  No matching models.\n")
	}
	b.WriteString("\n  " + m.tr("hint_filter"))
	return b.String()
}

func (m *onboardModel) viewOptional() string {
	var b strings.Builder
	b.WriteString(m.tr("step_optional") + ":\n\n")

	// Permission mode with color + description
	cursor := "  "
	if m.optCursor == 0 {
		cursor = "> "
	}
	modeLabel := lipgloss.NewStyle().Foreground(modeColors[m.optMode]).Bold(true).Render(modeLabels[m.optMode])
	modeDesc := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.tr("mode_" + modeLabels[m.optMode]))
	b.WriteString(fmt.Sprintf("  %s%s %s  %s\n", cursor, m.tr("permission_mode"), modeLabel, modeDesc))

	// Knight
	cursor = "  "
	if m.optCursor == 1 {
		cursor = "> "
	}
	onOff := m.tr("off")
	if m.optKnight {
		onOff = m.tr("on")
	}
	b.WriteString(fmt.Sprintf("  %s%s %s\n", cursor, m.tr("knight"), onOff))

	// A2A
	cursor = "  "
	if m.optCursor == 2 {
		cursor = "> "
	}
	onOff = m.tr("off")
	if m.optA2A {
		onOff = m.tr("on")
	}
	b.WriteString(fmt.Sprintf("  %s%s %s\n", cursor, m.tr("a2a"), onOff))
	return b.String()
}

func (m *onboardModel) viewIM() string {
	var b strings.Builder
	b.WriteString(m.tr("step_im") + ":\n\n")

	// Telegram — single bot_token input
	b.WriteString(m.viewIMChannel(0, m.tr("im_telegram"), m.tr("im_telegram_hint")))

	// Discord — single bot_token input
	b.WriteString(m.viewIMChannel(1, m.tr("im_discord"), m.tr("im_discord_hint")))

	// QQ — app_id + app_secret
	focus := "  "
	if m.imCursor == 2 {
		focus = "▸ "
	}
	b.WriteString(fmt.Sprintf("%s%s\n", focus, m.tr("im_qq")))
	b.WriteString("  " + m.tr("im_qq_appid") + " " + m.imInputs[2].View() + "\n")
	b.WriteString("  " + m.tr("im_qq_secret") + " " + m.imInputs[3].View() + "\n")
	b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.tr("im_qq_hint")) + "\n\n")

	// WeChat — QR scan only, no input
	focus = "  "
	if m.imCursor == 3 {
		focus = "> "
	}
	b.WriteString(fmt.Sprintf("%s%s\n", focus, m.tr("im_wechat")))
	b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.tr("im_wechat_hint")) + "\n\n")

	b.WriteString(m.tr("im_more"))
	return b.String()
}

func (m *onboardModel) viewIMChannel(idx int, label string, hint string) string {
	var b strings.Builder
	focus := "  "
	if m.imCursor == idx && m.imFocused != idx {
		focus = "> "
	}
	if m.imFocused == idx {
		focus = "▸ "
	}
	b.WriteString(fmt.Sprintf("%s%s\n", focus, label))
	b.WriteString("  " + m.imInputs[idx].View() + "\n")
	b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(hint) + "\n\n")
	return b.String()
}

func (m *onboardModel) viewCustom() string {
	var b strings.Builder
	b.WriteString(m.tr("step_custom") + ":\n\n")

	// Protocol selector
	protoCursor := "  "
	if m.customCursor == 0 {
		protoCursor = "> "
	}
	protoName := customProtocols[m.customProtocolIdx]
	b.WriteString(fmt.Sprintf("  %s%s %s ←→\n", protoCursor, m.tr("custom_protocol"), protoName))

	// Name
	nameCursor := "  "
	if m.customCursor == 1 {
		nameCursor = "▸ "
	}
	b.WriteString(fmt.Sprintf("\n  %s%s\n", nameCursor, m.tr("custom_name")))
	b.WriteString("    " + m.customFields[0].View() + "\n")

	// URL
	urlCursor := "  "
	if m.customCursor == 2 {
		urlCursor = "▸ "
	}
	b.WriteString(fmt.Sprintf("\n  %s%s\n", urlCursor, m.tr("custom_url")))
	b.WriteString("    " + m.customFields[1].View() + "\n")

	// API Key
	keyCursor := "  "
	if m.customCursor == 3 {
		keyCursor = "▸ "
	}
	b.WriteString(fmt.Sprintf("\n  %s%s\n", keyCursor, m.tr("custom_apikey")))
	b.WriteString("    " + m.customFields[2].View() + "\n")

	// Model
	modelCursor := "  "
	if m.customCursor == 4 {
		modelCursor = "▸ "
	}
	b.WriteString(fmt.Sprintf("\n  %s%s\n", modelCursor, m.tr("custom_model")))
	b.WriteString("    " + m.customFields[3].View() + "\n")

	// Submit
	submitCursor := "  "
	if m.customCursor == 5 {
		submitCursor = "> "
	}
	b.WriteString(fmt.Sprintf("\n  %s%s\n", submitCursor, m.tr("custom_submit")))
	b.WriteString("\n  " + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(m.tr("custom_hint")))
	return b.String()
}
