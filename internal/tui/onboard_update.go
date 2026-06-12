package tui

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/provider"
)

func (m *onboardModel) updateLanguage(msg tea.Msg) (tea.Model, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch kp.String() {
	case "up", "k":
		if m.langCursor > 0 {
			m.langCursor--
		}
	case "down", "j":
		if m.langCursor < len(m.langs)-1 {
			m.langCursor++
		}
	case "enter":
		m.refreshInputPlaceholders()
		m.step = onboardStepVendor
		m.vendorFilter.Focus()
		return m, textinput.Blink
	}
	m.refreshInputPlaceholders()
	return m, nil
}

func (m *onboardModel) updateVendor(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.vendorFilter.Focused() {
		switch kp := msg.(type) {
		case tea.KeyPressMsg:
			switch kp.String() {
			case "up", "down":
				if kp.String() == "up" && m.vendorCursor > 0 {
					m.vendorCursor--
				}
				if kp.String() == "down" && m.vendorCursor < len(m.vendorFiltered)-1 {
					m.vendorCursor++
				}
				return m, nil
			case "enter":
				if len(m.vendorFiltered) == 0 {
					return m, nil
				}
				m.selectVendor(m.vendorFiltered[m.vendorCursor])
				return m, nil
			case "tab":
				m.vendorFilter.Blur()
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.vendorFilter, cmd = m.vendorFilter.Update(msg)
		m.applyVendorFilter()
		if m.vendorCursor >= len(m.vendorFiltered) {
			m.vendorCursor = max(0, len(m.vendorFiltered)-1)
		}
		return m, cmd
	}

	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch kp.String() {
	case "up", "k":
		if m.vendorCursor > 0 {
			m.vendorCursor--
		}
	case "down", "j":
		if m.vendorCursor < len(m.vendorFiltered)-1 {
			m.vendorCursor++
		}
	case "enter":
		if len(m.vendorFiltered) == 0 {
			return m, nil
		}
		m.selectVendor(m.vendorFiltered[m.vendorCursor])
	case "/":
		m.vendorFilter.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m *onboardModel) applyVendorFilter() {
	q := strings.ToLower(m.vendorFilter.Value())
	m.vendorFiltered = m.vendorFiltered[:0]
	for i, p := range m.presets {
		if q == "" || strings.Contains(strings.ToLower(p.DisplayName), q) || strings.Contains(strings.ToLower(p.ID), q) {
			m.vendorFiltered = append(m.vendorFiltered, i)
		}
	}
}

func (m *onboardModel) selectVendor(idx int) {
	preset := m.presets[idx]
	m.selectedVendor = preset
	m.vendorFilter.Blur()
	m.endpointCursor = 0
	m.step = onboardStepEndpoint
	m.apiKeyInput.SetValue("")
	m.apiKeyInput.Blur()
	if len(preset.Endpoints) == 1 {
		m.epFocus = focusAPIKey
		m.apiKeyInput.Focus()
	} else {
		m.epFocus = focusEndpoint
	}
}

func (m *onboardModel) updateEndpoint(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.epFocus == focusAPIKey {
		switch kp := msg.(type) {
		case tea.KeyPressMsg:
			switch kp.String() {
			case "enter":
				m.apiKeyInput.Blur()
				m.err = ""
				return m, m.startModelSelection()
			case "tab":
				m.epFocus = focusEndpoint
				m.apiKeyInput.Blur()
				return m, nil
			case "up", "down":
				m.epFocus = focusEndpoint
				m.apiKeyInput.Blur()
				if kp.String() == "up" && m.endpointCursor > 0 {
					m.endpointCursor--
				}
				if kp.String() == "down" && m.endpointCursor < len(m.selectedVendor.Endpoints)-1 {
					m.endpointCursor++
				}
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
		return m, cmd
	}

	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch kp.String() {
	case "up", "k":
		if m.endpointCursor > 0 {
			m.endpointCursor--
		}
	case "down", "j":
		if m.endpointCursor < len(m.selectedVendor.Endpoints)-1 {
			m.endpointCursor++
		}
	case "tab":
		m.epFocus = focusAPIKey
		m.apiKeyInput.Focus()
		return m, textinput.Blink
	case "enter":
		m.epFocus = focusAPIKey
		m.apiKeyInput.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m *onboardModel) startModelSelection() tea.Cmd {
	m.step = onboardStepModel
	m.modelCursor = 0
	m.modelLoading = true

	ep := m.selectedVendor.Endpoints[m.endpointCursor]
	if len(ep.Models) > 0 {
		m.allModels = ep.Models
		m.models = ep.Models
	} else if ep.DefaultModel != "" {
		m.allModels = []string{ep.DefaultModel}
		m.models = []string{ep.DefaultModel}
	} else {
		m.allModels = []string{"default"}
		m.models = []string{"default"}
	}

	if len(m.models) > 20 {
		m.models = m.models[:20]
	}
	m.allModels = m.models
	m.applyModelFilter()

	for i, idx := range m.modelFiltered {
		if m.models[idx] == ep.DefaultModel {
			m.modelCursor = i
			break
		}
	}
	m.modelLoading = false

	resolved := m.buildResolved()
	if resolved != nil {
		return func() tea.Msg {
			models, err := provider.DiscoverModels(context.Background(), resolved)
			if err != nil || len(models) == 0 {
				return nil
			}
			return discoverResultMsg{models: models}
		}
	}
	return nil
}

func (m *onboardModel) updateModel(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.modelFilter.Focused() {
		switch kp := msg.(type) {
		case tea.KeyPressMsg:
			switch kp.String() {
			case "up", "down":
				if kp.String() == "up" && m.modelCursor > 0 {
					m.modelCursor--
				}
				if kp.String() == "down" && m.modelCursor < len(m.modelFiltered)-1 {
					m.modelCursor++
				}
				return m, nil
			case "enter":
				if len(m.modelFiltered) > 0 {
					m.step = onboardStepOptional
					m.modelFilter.Blur()
				}
				return m, nil
			case "tab":
				m.modelFilter.Blur()
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.modelFilter, cmd = m.modelFilter.Update(msg)
		m.applyModelFilter()
		if m.modelCursor >= len(m.modelFiltered) {
			m.modelCursor = max(0, len(m.modelFiltered)-1)
		}
		return m, cmd
	}

	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch kp.String() {
	case "up", "k":
		if m.modelCursor > 0 {
			m.modelCursor--
		}
	case "down", "j":
		if m.modelCursor < len(m.modelFiltered)-1 {
			m.modelCursor++
		}
	case "enter":
		if len(m.modelFiltered) > 0 {
			m.step = onboardStepOptional
		}
	case "/":
		m.modelFilter.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m *onboardModel) applyModelFilter() {
	q := strings.ToLower(m.modelFilter.Value())
	m.modelFiltered = m.modelFiltered[:0]
	for i, model := range m.models {
		if q == "" || strings.Contains(strings.ToLower(model), q) {
			m.modelFiltered = append(m.modelFiltered, i)
		}
	}
}

func (m *onboardModel) updateOptional(msg tea.Msg) (tea.Model, tea.Cmd) {
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch kp.String() {
	case "up", "k":
		if m.optCursor > 0 {
			m.optCursor--
		}
	case "down", "j":
		if m.optCursor < 2 {
			m.optCursor++
		}
	case "left", "h":
		m.toggleOpt(false)
	case "right", "l":
		m.toggleOpt(true)
	case " ":
		m.toggleOpt(true)
	case "enter":
		m.step = onboardStepIM
		m.imFocused = -1
		return m, nil
	case "s":
		m.step = onboardStepDone
		return m, tea.Quit
	}
	return m, nil
}

func (m *onboardModel) toggleOpt(forward bool) {
	switch m.optCursor {
	case 0:
		if forward {
			m.optMode = (m.optMode + 1) % len(modeLabels)
		} else {
			m.optMode = (m.optMode - 1 + len(modeLabels)) % len(modeLabels)
		}
	case 1:
		m.optKnight = !m.optKnight
	case 2:
		m.optA2A = !m.optA2A
	}
}

func (m *onboardModel) updateIM(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If an input is focused, handle typing
	if m.imFocused >= 0 && m.imFocused < len(m.imInputs) {
		switch kp := msg.(type) {
		case tea.KeyPressMsg:
			switch kp.String() {
			case "tab":
				m.imInputs[m.imFocused].Blur()
				// Special QQ handling: after app_id (idx 2), tab to app_secret (idx 3)
				if m.imFocused == 2 {
					m.imFocused = 3
					m.imInputs[3].Focus()
					return m, textinput.Blink
				}
				// After app_secret (idx 3), move to next channel (discord done → QQ → wechat skip)
				if m.imFocused == 3 {
					m.imFocused = -1
					m.imCursor = 3 // WeChat
					return m, nil
				}
				// Telegram(0)→Discord(1), Discord(1)→QQ appid(2)
				m.imFocused++
				if m.imFocused >= len(m.imInputs) {
					m.imFocused = -1
				} else {
					m.imInputs[m.imFocused].Focus()
					return m, textinput.Blink
				}
				return m, nil
			case "enter":
				m.imInputs[m.imFocused].Blur()
				m.imFocused = -1
				m.step = onboardStepDone
				return m, tea.Quit
			case "up":
				m.imInputs[m.imFocused].Blur()
				m.imFocused = -1
				return m, nil
			case "down":
				m.imInputs[m.imFocused].Blur()
				m.imFocused = -1
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.imInputs[m.imFocused], cmd = m.imInputs[m.imFocused].Update(msg)
		return m, cmd
	}

	// No input focused — arrow keys select channel, enter focuses
	kp, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch kp.String() {
	case "up", "k":
		if m.imCursor > 0 {
			m.imCursor--
		}
	case "down", "j":
		if m.imCursor < 3 { // 4 channels: telegram=0, discord=1, qq=2, wechat=3
			m.imCursor++
		}
	case "enter":
		switch m.imCursor {
		case 0: // Telegram
			m.imFocused = 0
			m.imInputs[0].Focus()
			return m, textinput.Blink
		case 1: // Discord
			m.imFocused = 1
			m.imInputs[1].Focus()
			return m, textinput.Blink
		case 2: // QQ → focus app_id
			m.imFocused = 2
			m.imInputs[2].Focus()
			return m, textinput.Blink
		case 3: // WeChat → skip (no input, just info)
			// Do nothing — user needs to use TUI for QR scan
		}
	case "s":
		m.step = onboardStepDone
		return m, tea.Quit
	}
	return m, nil
}
