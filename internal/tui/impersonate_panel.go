package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

type impSection int

const (
	impSectionPresets impSection = iota
	impSectionVersion
	impSectionHeaders
)

type headerEntry struct {
	Key   string
	Value string
}

type impersonatePanelState struct {
	presets          []provider.ImpersonationPreset
	cursor           int
	currentPreset    string
	versionInput     textinput.Model
	customHeaders    []headerEntry
	editingHeader    int // -1 = none, >=0 = editing index
	headerKeyInput   textinput.Model
	headerValueInput textinput.Model
	section          impSection
	message          string
	scrollOffset     int
}

func (m *Model) openImpersonatePanel() {
	m.providerPanel = nil
	m.modelPanel = nil
	m.mcpPanel = nil
	m.skillsPanel = nil

	presets := provider.DefaultImpersonationPresets()

	currentPreset := "none"
	var currentVersion string
	var customHeaders []headerEntry
	if m.config != nil {
		imp := m.config.Impersonation
		if imp.Preset != "" {
			currentPreset = imp.Preset
		}
		currentVersion = imp.CustomVersion
		for k, v := range imp.CustomHeaders {
			customHeaders = append(customHeaders, headerEntry{Key: k, Value: v})
		}
		sort.Slice(customHeaders, func(i, j int) bool {
			return customHeaders[i].Key < customHeaders[j].Key
		})
	}
	if currentVersion == "" {
		for _, p := range presets {
			if p.ID == currentPreset {
				currentVersion = p.DefaultVersion
				break
			}
		}
	}

	cursor := 0
	for i, p := range presets {
		if p.ID == currentPreset {
			cursor = i
			break
		}
	}

	vi := textinput.New()
	vi.Prompt = "> "
	vi.Placeholder = "version"
	vi.CharLimit = 64
	vi.SetValue(currentVersion)

	ki := textinput.New()
	ki.Prompt = ""
	ki.Placeholder = "header name"
	ki.CharLimit = 64

	hvi := textinput.New()
	hvi.Prompt = ""
	hvi.Placeholder = "header value"
	hvi.CharLimit = 256

	m.impersonatePanel = &impersonatePanelState{
		presets:          presets,
		cursor:           cursor,
		currentPreset:    currentPreset,
		versionInput:     vi,
		customHeaders:    customHeaders,
		editingHeader:    -1,
		headerKeyInput:   ki,
		headerValueInput: hvi,
		section:          impSectionPresets,
		message:          "",
		scrollOffset:     0,
	}
}

func (m *Model) closeImpersonatePanel() {
	m.impersonatePanel = nil
}

func (m Model) renderImpersonatePanel() string {
	panel := m.impersonatePanel
	if panel == nil {
		return ""
	}

	var body []string

	// --- Presets section ---
	sectionStyle := lipgloss.NewStyle().Bold(true)
	body = append(body, sectionStyle.Render(" Identity Presets"))
	body = append(body, "")

	maxVisible := 8
	start, end := panel.scrollOffset, panel.scrollOffset+maxVisible
	if end > len(panel.presets) {
		end = len(panel.presets)
	}
	for i := start; i < end; i++ {
		p := panel.presets[i]
		prefix := "  "
		if panel.section == impSectionPresets && i == panel.cursor {
			prefix = "> "
		}
		active := ""
		if p.ID == panel.currentPreset {
			active = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(" [active]")
		}
		name := p.DisplayName
		if panel.section == impSectionPresets && i == panel.cursor {
			name = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(name)
		}
		body = append(body, fmt.Sprintf("%s%s%s", prefix, name, active))
	}
	if len(panel.presets) > maxVisible {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
			fmt.Sprintf("  (%d/%d, scroll with j/k)", panel.cursor+1, len(panel.presets))))
	}

	// --- Version section ---
	body = append(body, "")
	versionLabel := " Version"
	if panel.section == impSectionVersion {
		versionLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render(" Version")
	}
	body = append(body, versionLabel)
	if panel.section == impSectionVersion {
		body = append(body, "  "+panel.versionInput.View())
	} else {
		v := panel.versionInput.Value()
		if v == "" {
			v = "(default)"
		}
		body = append(body, fmt.Sprintf("  %s", v))
	}

	// --- Custom Headers section ---
	body = append(body, "")
	headersLabel := " Custom Headers"
	if panel.section == impSectionHeaders {
		headersLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Render(" Custom Headers")
	}
	body = append(body, headersLabel)
	if len(panel.customHeaders) == 0 && panel.editingHeader < 0 {
		body = append(body, "  (none)")
	}
	for i, h := range panel.customHeaders {
		prefix := "  "
		if panel.section == impSectionHeaders && i == panel.editingHeader {
			prefix = "> "
		}
		body = append(body, fmt.Sprintf("%s%s: %s", prefix, h.Key, h.Value))
	}
	if panel.editingHeader >= 0 {
		body = append(body, fmt.Sprintf("  key:   %s", panel.headerKeyInput.View()))
		body = append(body, fmt.Sprintf("  value: %s", panel.headerValueInput.View()))
	}

	// --- Hint ---
	body = append(body, "")
	hint := "j/k navigate  enter select  tab sections  a add header  d delete  esc close"
	body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+hint))

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}

	return m.renderContextBox("/impersonate", strings.Join(body, "\n"), lipgloss.Color("13"))
}

func (m *Model) handleImpersonatePanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.impersonatePanel
	if panel == nil {
		return *m, nil
	}

	// If editing a header, route to header input
	if panel.editingHeader >= 0 {
		return m.handleImpersonateHeaderEdit(msg)
	}

	// If version section is focused and input is focused
	if panel.section == impSectionVersion && panel.versionInput.Focused() {
		switch msg.String() {
		case "esc", "enter":
			panel.versionInput.Blur()
			return *m, nil
		default:
			var cmd tea.Cmd
			panel.versionInput, cmd = panel.versionInput.Update(msg)
			return *m, cmd
		}
	}

	switch msg.String() {
	case "up", "k":
		if panel.section == impSectionPresets && panel.cursor > 0 {
			panel.cursor--
			if panel.cursor < panel.scrollOffset {
				panel.scrollOffset = panel.cursor
			}
		}
		return *m, nil

	case "down", "j":
		if panel.section == impSectionPresets && panel.cursor < len(panel.presets)-1 {
			panel.cursor++
			if panel.cursor >= panel.scrollOffset+8 {
				panel.scrollOffset = panel.cursor - 7
			}
		}
		return *m, nil

	case "tab":
		switch panel.section {
		case impSectionPresets:
			panel.section = impSectionVersion
			panel.versionInput.Focus()
		case impSectionVersion:
			panel.versionInput.Blur()
			panel.section = impSectionHeaders
		case impSectionHeaders:
			panel.section = impSectionPresets
		}
		return *m, nil

	case "enter":
		if panel.section == impSectionPresets {
			return m.applyImpersonatePreset()
		}
		return *m, nil

	case "a":
		if panel.section == impSectionHeaders {
			panel.editingHeader = len(panel.customHeaders)
			panel.headerKeyInput.SetValue("")
			panel.headerValueInput.SetValue("")
			panel.headerKeyInput.Focus()
			return *m, nil
		}
		return *m, nil

	case "d":
		if panel.section == impSectionHeaders && len(panel.customHeaders) > 0 {
			panel.customHeaders = panel.customHeaders[1:]
		}
		return *m, nil

	case "esc":
		m.closeImpersonatePanel()
		return *m, nil
	}

	return *m, nil
}

func (m *Model) handleImpersonateHeaderEdit(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.impersonatePanel
	if panel == nil {
		return *m, nil
	}

	switch msg.String() {
	case "enter":
		key := strings.TrimSpace(panel.headerKeyInput.Value())
		val := strings.TrimSpace(panel.headerValueInput.Value())
		if key == "" {
			panel.message = "header name is required"
			panel.editingHeader = -1
			return *m, nil
		}
		replaced := false
		for i, h := range panel.customHeaders {
			if strings.EqualFold(h.Key, key) {
				panel.customHeaders[i].Value = val
				replaced = true
				break
			}
		}
		if !replaced {
			panel.customHeaders = append(panel.customHeaders, headerEntry{Key: key, Value: val})
			sort.Slice(panel.customHeaders, func(i, j int) bool {
				return panel.customHeaders[i].Key < panel.customHeaders[j].Key
			})
		}
		panel.editingHeader = -1
		panel.headerKeyInput.Blur()
		panel.headerValueInput.Blur()
		return *m, m.applyImpersonateSettings()
	case "tab":
		if panel.headerKeyInput.Focused() {
			panel.headerKeyInput.Blur()
			panel.headerValueInput.Focus()
		} else {
			panel.headerValueInput.Blur()
			panel.headerKeyInput.Focus()
		}
		return *m, nil
	case "esc":
		panel.editingHeader = -1
		panel.headerKeyInput.Blur()
		panel.headerValueInput.Blur()
		return *m, nil
	default:
		if panel.headerKeyInput.Focused() {
			var cmd tea.Cmd
			panel.headerKeyInput, cmd = panel.headerKeyInput.Update(msg)
			return *m, cmd
		}
		if panel.headerValueInput.Focused() {
			var cmd tea.Cmd
			panel.headerValueInput, cmd = panel.headerValueInput.Update(msg)
			return *m, cmd
		}
	}
	return *m, nil
}

func (m *Model) applyImpersonatePreset() (Model, tea.Cmd) {
	panel := m.impersonatePanel
	if panel == nil || m.config == nil {
		return *m, nil
	}

	preset := panel.presets[panel.cursor]
	panel.currentPreset = preset.ID

	// Apply and persist
	m.applyImpersonateSettings()

	presetPtr := &preset
	if preset.ID == "none" {
		presetPtr = nil
	}
	if presetPtr == nil {
		panel.message = "cleared impersonation"
	} else {
		panel.message = fmt.Sprintf("set identity: %s", preset.DisplayName)
	}

	return *m, nil
}

func (m *Model) applyImpersonateSettings() tea.Cmd {
	panel := m.impersonatePanel
	if panel == nil || m.config == nil {
		return nil
	}

	presetID := panel.currentPreset
	var presetPtr *provider.ImpersonationPreset
	if presetID != "" && presetID != "none" {
		presetPtr = provider.FindPresetByID(presetID)
	}

	version := strings.TrimSpace(panel.versionInput.Value())
	customHeaders := headerEntriesToMap(panel.customHeaders)

	// Set global impersonation state
	provider.SetActiveImpersonation(presetPtr, version, customHeaders)

	// Apply headers to current provider
	m.applyImpersonationToProvider()

	// Persist to config
	impCfg := config.ImpersonationConfig{
		Preset:        presetID,
		CustomVersion: version,
		CustomHeaders: customHeaders,
	}
	if err := m.config.SaveImpersonation(impCfg); err != nil {
		panel.message = fmt.Sprintf("config write failed: %v", err)
	}

	return nil
}

func (m *Model) applyImpersonationToProvider() {
	if m.agent == nil {
		return
	}
	p := m.agent.Provider()
	if p == nil {
		return
	}

	headers := provider.BuildHeadersForProvider(m.currentProtocol())
	if mutable, ok := p.(provider.HeaderMutable); ok {
		mutable.UpdateRuntimeHeaders(headers)
	}

	// For copilot provider, also set the UA directly
	if copilot, ok := p.(interface{ SetImpersonatedUA(string) }); ok {
		ua := headers.Get("User-Agent")
		if ua != "" {
			copilot.SetImpersonatedUA(ua)
		}
	}

	// For Anthropic provider, headers are immutable - need to reload
	if p.Name() == "anthropic" {
		_ = m.reloadActiveProvider()
	}
}

func (m *Model) currentProtocol() string {
	if m.config == nil {
		return "openai"
	}
	ep := m.config.ActiveEndpointConfig()
	if ep != nil {
		return ep.Protocol
	}
	return "openai"
}

func headerEntriesToMap(headers []headerEntry) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	result := make(map[string]string, len(headers))
	for _, h := range headers {
		result[h.Key] = h.Value
	}
	return result
}
