package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type providerPanelState struct {
	focus         int
	vendorIDs     []string
	vendorIndex   int
	endpointIDs   []string
	endpointIndex int
	models        []string
	modelIndex    int
	editingField  string
	editInput     textinput.Model
	message       string
}

const (
	providerPanelFocusVendor = iota
	providerPanelFocusEndpoint
	providerPanelFocusModel
)

func newProviderPanelFromConfig(cfg *ConfigView) *providerPanelState {
	vendorIDs := cfg.VendorNames()
	panel := &providerPanelState{
		vendorIDs: vendorIDs,
	}
	panel.selectVendor(cfg.Vendor)
	panel.selectEndpoint(cfg.Endpoint, cfg.Model, cfg)
	return panel
}

type ConfigView struct {
	Vendor            string
	Endpoint          string
	Model             string
	VendorNamesFunc   func() []string
	EndpointNamesFunc func(string) []string
	ModelsForEndpoint func(string, string) []string
}

func (c *ConfigView) VendorNames() []string {
	return c.VendorNamesFunc()
}

func (c *ConfigView) EndpointNames(vendor string) []string {
	return c.EndpointNamesFunc(vendor)
}

func (p *providerPanelState) selectVendor(vendor string) {
	p.vendorIndex = indexOf(p.vendorIDs, vendor)
	if p.vendorIndex < 0 {
		p.vendorIndex = 0
	}
}

func (p *providerPanelState) selectEndpoint(endpoint, model string, cfg *ConfigView) {
	p.endpointIDs = cfg.EndpointNames(p.selectedVendor())
	p.endpointIndex = indexOf(p.endpointIDs, endpoint)
	if p.endpointIndex < 0 {
		p.endpointIndex = 0
	}
	p.models = cfg.ModelsForEndpoint(p.selectedVendor(), p.selectedEndpoint())
	p.modelIndex = indexOf(p.models, model)
	if p.modelIndex < 0 {
		p.modelIndex = 0
	}
}

func (p *providerPanelState) syncLists(cfg *ConfigView) {
	p.endpointIDs = cfg.EndpointNames(p.selectedVendor())
	if p.endpointIndex >= len(p.endpointIDs) {
		p.endpointIndex = 0
	}
	p.models = cfg.ModelsForEndpoint(p.selectedVendor(), p.selectedEndpoint())
	if p.modelIndex >= len(p.models) {
		p.modelIndex = 0
	}
}

func (p *providerPanelState) selectedVendor() string {
	if len(p.vendorIDs) == 0 || p.vendorIndex < 0 || p.vendorIndex >= len(p.vendorIDs) {
		return ""
	}
	return p.vendorIDs[p.vendorIndex]
}

func (p *providerPanelState) selectedEndpoint() string {
	if len(p.endpointIDs) == 0 || p.endpointIndex < 0 || p.endpointIndex >= len(p.endpointIDs) {
		return ""
	}
	return p.endpointIDs[p.endpointIndex]
}

func (p *providerPanelState) selectedModel() string {
	if len(p.models) == 0 || p.modelIndex < 0 || p.modelIndex >= len(p.models) {
		return ""
	}
	return p.models[p.modelIndex]
}

func (p *providerPanelState) startEditing(field, initialValue string) {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.SetValue(initialValue)
	ti.CursorEnd()
	ti.Focus()
	p.editingField = field
	p.editInput = ti
}

func (m *Model) configView() *ConfigView {
	return &ConfigView{
		Vendor:   m.config.Vendor,
		Endpoint: m.config.Endpoint,
		Model:    m.config.Model,
		VendorNamesFunc: func() []string {
			return m.config.VendorNames()
		},
		EndpointNamesFunc: func(vendor string) []string {
			return m.config.EndpointNames(vendor)
		},
		ModelsForEndpoint: func(vendor, endpoint string) []string {
			if m.config == nil {
				return nil
			}
			vc, ok := m.config.Vendors[vendor]
			if !ok {
				return nil
			}
			ep, ok := vc.Endpoints[endpoint]
			if !ok {
				return nil
			}
			models := append([]string(nil), ep.Models...)
			if ep.SelectedModel != "" {
				models = append(models, ep.SelectedModel)
			}
			if ep.DefaultModel != "" {
				models = append(models, ep.DefaultModel)
			}
			models = uniqueStrings(models)
			if len(models) == 0 {
				models = []string{m.config.Model}
			}
			return models
		},
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (m *Model) openProviderPanel() {
	if m.config == nil {
		return
	}
	m.providerPanel = newProviderPanelFromConfig(m.configView())
}

func (m *Model) closeProviderPanel() {
	m.providerPanel = nil
}

func (m *Model) renderProviderPanel() string {
	panel := m.providerPanel
	if panel == nil || m.config == nil {
		return ""
	}

	vc := m.config.Vendors[panel.selectedVendor()]
	ep := vc.Endpoints[panel.selectedEndpoint()]
	apiKeyState := "missing"
	if strings.TrimSpace(firstNonEmptyValue(ep.APIKey, vc.APIKey)) != "" {
		apiKeyState = "configured"
	}
	baseURLState := ep.BaseURL
	if baseURLState == "" {
		baseURLState = "(not set)"
	}
	model := panel.selectedModel()
	if model == "" {
		model = "(set with m)"
	}

	body := []string{
		lipgloss.NewStyle().Bold(true).Render(" Vendors"),
		m.renderProviderList(panel.vendorIDs, panel.vendorIndex, panel.focus == providerPanelFocusVendor),
		"",
		lipgloss.NewStyle().Bold(true).Render(" Endpoints"),
		m.renderProviderList(panel.endpointIDs, panel.endpointIndex, panel.focus == providerPanelFocusEndpoint),
		"",
		lipgloss.NewStyle().Bold(true).Render(" Models"),
		m.renderProviderList(panel.models, panel.modelIndex, panel.focus == providerPanelFocusModel),
		"",
		fmt.Sprintf(" Active draft: %s / %s / %s", panel.selectedVendor(), panel.selectedEndpoint(), model),
		fmt.Sprintf(" Protocol: %s", firstNonEmptyValue(ep.Protocol, "(unknown)")),
		fmt.Sprintf(" API key: %s", apiKeyState),
		fmt.Sprintf(" Base URL: %s", baseURLState),
		fmt.Sprintf(" Tags: %s", strings.Join(ep.Tags, ", ")),
	}
	if panel.editingField != "" {
		body = append(body,
			"",
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" Edit "+panel.editingField),
			panel.editInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Enter save • Esc cancel"),
		)
	} else {
		body = append(body,
			"",
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Tab/Shift+Tab change focus • j/k move • Enter or s apply • a vendor key • u endpoint key • b base URL • m custom model • Esc close"),
		)
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}

	return m.renderContextBox("/provider", strings.Join(body, "\n"), lipgloss.Color("14"))
}

func (m *Model) renderProviderList(items []string, selected int, focused bool) string {
	if len(items) == 0 {
		return "  (none)"
	}
	rows := make([]string, 0, len(items))
	for i, item := range items {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == selected {
			prefix = "❯ "
			if focused {
				style = style.Foreground(lipgloss.Color("226")).Background(lipgloss.Color("236"))
			} else {
				style = style.Foreground(lipgloss.Color("14"))
			}
		} else if focused {
			style = style.Foreground(lipgloss.Color("8"))
		}
		rows = append(rows, style.Render(prefix+item))
	}
	return strings.Join(rows, "\n")
}

func (m *Model) handleProviderPanelKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	panel := m.providerPanel
	if panel == nil {
		return *m, nil
	}
	if panel.editingField != "" {
		switch msg.String() {
		case "esc":
			panel.editingField = ""
			panel.message = ""
			return *m, nil
		case "enter":
			value := strings.TrimSpace(panel.editInput.Value())
			var err error
			switch panel.editingField {
			case "vendor api key":
				err = m.config.SetEndpointAPIKey(panel.selectedVendor(), panel.selectedEndpoint(), value, true)
			case "endpoint api key":
				err = m.config.SetEndpointAPIKey(panel.selectedVendor(), panel.selectedEndpoint(), value, false)
			case "endpoint base url":
				vc := m.config.Vendors[panel.selectedVendor()]
				ep := vc.Endpoints[panel.selectedEndpoint()]
				ep.BaseURL = value
				vc.Endpoints[panel.selectedEndpoint()] = ep
				m.config.Vendors[panel.selectedVendor()] = vc
			case "custom model":
				vc := m.config.Vendors[panel.selectedVendor()]
				ep := vc.Endpoints[panel.selectedEndpoint()]
				ep.SelectedModel = value
				ep.Models = uniqueStrings(append(ep.Models, value))
				vc.Endpoints[panel.selectedEndpoint()] = ep
				m.config.Vendors[panel.selectedVendor()] = vc
				panel.models = uniqueStrings(append(panel.models, value))
				panel.modelIndex = indexOf(panel.models, value)
			}
			if err != nil {
				panel.message = err.Error()
				return *m, nil
			}
			if err := m.config.Save(); err != nil {
				panel.message = err.Error()
				return *m, nil
			}
			panel.editingField = ""
			panel.message = "Saved."
			return *m, nil
		default:
			var cmd tea.Cmd
			panel.editInput, cmd = panel.editInput.Update(msg)
			return *m, cmd
		}
	}

	cfgView := m.configView()
	switch msg.String() {
	case "esc":
		m.closeProviderPanel()
		return *m, nil
	case "tab", "right":
		panel.focus = (panel.focus + 1) % 3
		return *m, nil
	case "shift+tab", "left":
		panel.focus = (panel.focus + 2) % 3
		return *m, nil
	case "up", "k":
		switch panel.focus {
		case providerPanelFocusVendor:
			if len(panel.vendorIDs) > 0 {
				panel.vendorIndex = (panel.vendorIndex - 1 + len(panel.vendorIDs)) % len(panel.vendorIDs)
				panel.endpointIndex = 0
				panel.modelIndex = 0
				panel.syncLists(cfgView)
			}
		case providerPanelFocusEndpoint:
			if len(panel.endpointIDs) > 0 {
				panel.endpointIndex = (panel.endpointIndex - 1 + len(panel.endpointIDs)) % len(panel.endpointIDs)
				panel.modelIndex = 0
				panel.syncLists(cfgView)
			}
		case providerPanelFocusModel:
			if len(panel.models) > 0 {
				panel.modelIndex = (panel.modelIndex - 1 + len(panel.models)) % len(panel.models)
			}
		}
		return *m, nil
	case "down", "j":
		switch panel.focus {
		case providerPanelFocusVendor:
			if len(panel.vendorIDs) > 0 {
				panel.vendorIndex = (panel.vendorIndex + 1) % len(panel.vendorIDs)
				panel.endpointIndex = 0
				panel.modelIndex = 0
				panel.syncLists(cfgView)
			}
		case providerPanelFocusEndpoint:
			if len(panel.endpointIDs) > 0 {
				panel.endpointIndex = (panel.endpointIndex + 1) % len(panel.endpointIDs)
				panel.modelIndex = 0
				panel.syncLists(cfgView)
			}
		case providerPanelFocusModel:
			if len(panel.models) > 0 {
				panel.modelIndex = (panel.modelIndex + 1) % len(panel.models)
			}
		}
		return *m, nil
	case "a":
		vc := m.config.Vendors[panel.selectedVendor()]
		panel.startEditing("vendor api key", vc.APIKey)
		return *m, nil
	case "u":
		vc := m.config.Vendors[panel.selectedVendor()]
		ep := vc.Endpoints[panel.selectedEndpoint()]
		panel.startEditing("endpoint api key", ep.APIKey)
		return *m, nil
	case "b":
		vc := m.config.Vendors[panel.selectedVendor()]
		ep := vc.Endpoints[panel.selectedEndpoint()]
		panel.startEditing("endpoint base url", ep.BaseURL)
		return *m, nil
	case "m":
		panel.startEditing("custom model", panel.selectedModel())
		return *m, nil
	case "enter", "s":
		if err := m.config.SetActiveSelection(panel.selectedVendor(), panel.selectedEndpoint(), panel.selectedModel()); err != nil {
			panel.message = err.Error()
			return *m, nil
		}
		if err := m.config.Save(); err != nil {
			panel.message = err.Error()
			return *m, nil
		}
		m.syncSessionSelection()
		if err := m.tryActivateCurrentSelection(); err != nil {
			panel.message = "Saved config, but current runtime is still inactive: " + err.Error()
			return *m, nil
		}
		panel.message = "Saved and activated."
		return *m, nil
	}
	return *m, nil
}
