package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// OnboardResult holds the user's selections from the onboard wizard.
type OnboardResult struct {
	Language   string
	VendorID   string
	EndpointID string
	APIKey     string
	Model      string
	Mode       string
	Knight     bool
	A2A        bool
}

type onboardStep int

const (
	onboardStepLanguage onboardStep = iota
	onboardStepVendor
	onboardStepAPIKey
	onboardStepModel
	onboardStepOptional
	onboardStepDone
)

type onboardModel struct {
	cfg     *config.Config
	presets []config.VendorPreset
	step    onboardStep
	width   int
	height  int
	err     string

	// Step 1: Language
	langCursor int
	langs      []struct {
		code string
		name string
	}

	// Step 2: Vendor
	vendorCursor   int
	vendorFilter   textinput.Model
	vendorFiltered []int // indices into presets

	// Step 3: API Key
	apiKeyInput      textinput.Model
	selectedVendor   config.VendorPreset
	selectedEndpoint config.EndpointPreset

	// Step 4: Model
	modelCursor  int
	models       []string
	modelLoading bool

	// Step 5: Optional
	optCursor int
	optMode   int // 0=supervised, 1=auto, 2=bypass, 3=autopilot
	optKnight bool
	optA2A    bool
}

var modeLabels = []string{"supervised", "auto", "bypass", "autopilot"}

// RunOnboard starts the onboard wizard as an independent Bubble Tea program.
// Returns the user's selections or an error.
func RunOnboard(cfg *config.Config) (*OnboardResult, error) {
	presets := config.VendorPresets()
	langs := []struct {
		code string
		name string
	}{
		{"en", "English"},
		{"zh-CN", "中文"},
	}

	// Vendor filter input
	vf := textinput.New()
	vf.Prompt = "> "
	vf.Placeholder = "Type to filter vendors..."

	// API key input
	ak := textinput.New()
	ak.Prompt = "> "
	ak.EchoMode = textinput.EchoPassword
	ak.EchoCharacter = '•'
	ak.Placeholder = "Enter your API key..."

	// Build initial filtered list
	filtered := make([]int, len(presets))
	for i := range presets {
		filtered[i] = i
	}

	m := onboardModel{
		cfg:            cfg,
		presets:        presets,
		langs:          langs,
		vendorFilter:   vf,
		vendorFiltered: filtered,
		apiKeyInput:    ak,
	}

	p := tea.NewProgram(&m)
	result, err := p.Run()
	if err != nil {
		return nil, err
	}
	if om, ok := result.(*onboardModel); ok && om.step == onboardStepDone {
		r := &OnboardResult{
			Language:   om.langs[om.langCursor].code,
			VendorID:   om.selectedVendor.ID,
			EndpointID: om.selectedEndpoint.ID,
			APIKey:     om.apiKeyInput.Value(),
			Model:      om.models[om.modelCursor],
			Mode:       modeLabels[om.optMode],
			Knight:     om.optKnight,
			A2A:        om.optA2A,
		}
		return r, nil
	}
	return nil, fmt.Errorf("onboard cancelled")
}

func (m *onboardModel) Init() tea.Cmd {
	return nil
}

func (m *onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			if m.step == onboardStepLanguage {
				return m, tea.Quit
			}
			// Go back one step
			if m.step > onboardStepLanguage {
				m.step--
				m.err = ""
				if m.step == onboardStepVendor {
					m.vendorFilter.Focus()
				}
				if m.step == onboardStepAPIKey {
					m.apiKeyInput.Focus()
				}
				return m, nil
			}
			return m, tea.Quit
		}
	}

	switch m.step {
	case onboardStepLanguage:
		return m.updateLanguage(msg)
	case onboardStepVendor:
		return m.updateVendor(msg)
	case onboardStepAPIKey:
		return m.updateAPIKey(msg)
	case onboardStepModel:
		return m.updateModel(msg)
	case onboardStepOptional:
		return m.updateOptional(msg)
	}
	return m, nil
}

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
		m.step = onboardStepVendor
		m.vendorFilter.Focus()
		return m, textinput.Blink
	}
	return m, nil
}

func (m *onboardModel) updateVendor(msg tea.Msg) (tea.Model, tea.Cmd) {
	// If filter is focused, handle text input
	if m.vendorFilter.Focused() {
		switch kp := msg.(type) {
		case tea.KeyPressMsg:
			switch kp.String() {
			case "up", "down":
				// Navigate without unfocusing filter
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
		// Keep cursor in bounds
		if m.vendorCursor >= len(m.vendorFiltered) {
			m.vendorCursor = max(0, len(m.vendorFiltered)-1)
		}
		return m, cmd
	}

	// Filter not focused — arrow keys + enter
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
	// Pick first endpoint
	if len(preset.Endpoints) > 0 {
		m.selectedEndpoint = preset.Endpoints[0]
	}
	m.vendorFilter.Blur()

	if preset.NeedsAPIKey {
		m.step = onboardStepAPIKey
		m.apiKeyInput.SetValue("")
		m.apiKeyInput.Focus()
	} else {
		// No API key needed, skip to model selection
		m.apiKeyInput.SetValue("")
		m.startModelSelection()
	}
}

func (m *onboardModel) updateAPIKey(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch kp := msg.(type) {
	case tea.KeyPressMsg:
		switch kp.String() {
		case "enter":
			val := strings.TrimSpace(m.apiKeyInput.Value())
			if m.selectedVendor.NeedsAPIKey && val == "" {
				m.err = "API key is required"
				return m, nil
			}
			m.apiKeyInput.Blur()
			m.err = ""
			m.startModelSelection()
			return m, nil
		case "tab":
			// Skip (for vendors that don't strictly need a key)
			m.apiKeyInput.Blur()
			m.err = ""
			m.startModelSelection()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.apiKeyInput, cmd = m.apiKeyInput.Update(msg)
	return m, cmd
}

func (m *onboardModel) startModelSelection() {
	m.step = onboardStepModel
	m.modelCursor = 0
	m.modelLoading = true

	// Try API discovery first
	resolved := m.buildResolved()
	if resolved != nil {
		go func() {
			models, err := provider.DiscoverModels(context.Background(), resolved)
			if err == nil && len(models) > 0 {
				// Send result back to TUI
				fmt.Fprintf(os.Stderr, "\x1b]7777;models;%s\x07", strings.Join(models, ","))
			}
		}()
	}

	// Fallback: use static models from preset
	if len(m.selectedEndpoint.Models) > 0 {
		m.models = m.selectedEndpoint.Models
	} else if m.selectedEndpoint.DefaultModel != "" {
		m.models = []string{m.selectedEndpoint.DefaultModel}
	} else {
		m.models = []string{"default"}
	}
	// Select default model if present
	for i, model := range m.models {
		if model == m.selectedEndpoint.DefaultModel {
			m.modelCursor = i
			break
		}
	}
	m.modelLoading = false
}

func (m *onboardModel) updateModel(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.modelCursor < len(m.models)-1 {
			m.modelCursor++
		}
	case "enter":
		m.step = onboardStepOptional
		return m, nil
	}
	return m, nil
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
		if m.optCursor < 3 {
			m.optCursor++
		}
	case "left", "h":
		m.toggleOpt(false)
	case "right", "l":
		m.toggleOpt(true)
	case " ":
		m.toggleOpt(true)
	case "enter":
		m.step = onboardStepDone
		return m, tea.Quit
	case "s":
		// Skip — use defaults
		m.step = onboardStepDone
		return m, tea.Quit
	}
	return m, nil
}

func (m *onboardModel) toggleOpt(forward bool) {
	switch m.optCursor {
	case 0: // mode
		if forward {
			m.optMode = (m.optMode + 1) % len(modeLabels)
		} else {
			m.optMode = (m.optMode - 1 + len(modeLabels)) % len(modeLabels)
		}
	case 1: // knight
		m.optKnight = !m.optKnight
	case 2: // a2a
		m.optA2A = !m.optA2A
	}
}

func (m *onboardModel) buildResolved() *config.ResolvedEndpoint {
	ep := m.selectedEndpoint
	vp := m.selectedVendor
	apiKey := strings.TrimSpace(m.apiKeyInput.Value())
	if apiKey == "" {
		apiKey = vp.APIKeyEnvHint
	}
	return &config.ResolvedEndpoint{
		VendorID:     vp.ID,
		VendorName:   vp.DisplayName,
		EndpointID:   ep.ID,
		EndpointName: ep.DisplayName,
		Protocol:     ep.Protocol,
		AuthType:     "api_key",
		BaseURL:      ep.DefaultModel, // not quite right, need actual baseURL
		APIKey:       apiKey,
		Model:        ep.DefaultModel,
		Models:       ep.Models,
	}
}

// --- Rendering ---

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
	case onboardStepAPIKey:
		content = m.viewAPIKey()
	case onboardStepModel:
		content = m.viewModel()
	case onboardStepOptional:
		content = m.viewOptional()
	case onboardStepDone:
		return tea.NewView("")
	}

	// Center the content
	header := lipgloss.NewStyle().Bold(true).Render("🚀 ggcode setup")
	stepLabel := fmt.Sprintf("[%d/5]", int(m.step)+1)
	if m.step == onboardStepOptional {
		stepLabel = "[5/5] optional"
	}

	top := lipgloss.NewStyle().
		Width(m.width).
		Padding(1, 0).
		Align(lipgloss.Center).
		Render(header + "  " + stepLabel)

	bottom := ""
	if m.err != "" {
		bottom = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(m.err)
	}
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")).
		Render("↑↓ navigate · Enter select · Esc back · Ctrl+C quit")

	return tea.NewView(lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(top + "\n\n" + content + "\n\n" + bottom + "\n" + hint))
}

func (m *onboardModel) viewLanguage() string {
	var b strings.Builder
	b.WriteString("Select your language / 选择语言:\n\n")
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
	b.WriteString("Choose your AI provider:\n\n")
	if m.vendorFilter.Value() != "" || m.vendorFilter.Focused() {
		b.WriteString(fmt.Sprintf("Filter: %s\n\n", m.vendorFilter.View()))
	}
	for i, idx := range m.vendorFiltered {
		p := m.presets[idx]
		cursor := "  "
		if i == m.vendorCursor {
			cursor = "> "
		}
		hint := ""
		if p.APIKeyEnvHint != "" {
			hint = fmt.Sprintf("  (%s)", p.APIKeyEnvHint)
		}
		b.WriteString(fmt.Sprintf("  %s%s%s\n", cursor, p.DisplayName, hint))
	}
	if len(m.vendorFiltered) == 0 {
		b.WriteString("  No matching vendors.\n")
	}
	b.WriteString(fmt.Sprintf("\n  Press / to filter"))
	return b.String()
}

func (m *onboardModel) viewAPIKey() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Enter API key for %s:\n\n", m.selectedVendor.DisplayName))
	if m.selectedVendor.APIKeyEnvHint != "" {
		b.WriteString(fmt.Sprintf("  You can also set the %s environment variable.\n\n", m.selectedVendor.APIKeyEnvHint))
	}
	b.WriteString(m.apiKeyInput.View())
	b.WriteString("\n\n  Enter confirm · Tab skip")
	return b.String()
}

func (m *onboardModel) viewModel() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Select a model for %s:\n\n", m.selectedEndpoint.DisplayName))
	if m.modelLoading {
		b.WriteString("  Loading models...\n")
	}
	for i, model := range m.models {
		cursor := "  "
		if i == m.modelCursor {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("  %s%s\n", cursor, model))
	}
	return b.String()
}

func (m *onboardModel) viewOptional() string {
	var b strings.Builder
	b.WriteString("Optional settings (press S to skip):\n\n")

	// Mode
	cursor := "  "
	if m.optCursor == 0 {
		cursor = "> "
	}
	b.WriteString(fmt.Sprintf("  %sPermission mode: ← %s →\n", cursor, modeLabels[m.optMode]))

	// Knight
	cursor = "  "
	if m.optCursor == 1 {
		cursor = "> "
	}
	onOff := "off"
	if m.optKnight {
		onOff = "on"
	}
	b.WriteString(fmt.Sprintf("  %sKnight agent:     %s\n", cursor, onOff))

	// A2A
	cursor = "  "
	if m.optCursor == 2 {
		cursor = "> "
	}
	onOff = "off"
	if m.optA2A {
		onOff = "on"
	}
	b.WriteString(fmt.Sprintf("  %sA2A server:       %s\n", cursor, onOff))

	b.WriteString("\n  IM channels can be configured later from the TUI.")
	return b.String()
}
