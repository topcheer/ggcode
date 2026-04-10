package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

type providerPanelState struct {
	focus         int
	vendorIDs     []string
	vendorIndex   int
	endpointIDs   []string
	endpointIndex int
	models        []string
	modelIndex    int
	modelFilter   textinput.Model
	editingField  string
	editInput     textinput.Model
	message       string
	refreshing    bool
	refreshVendor string
}

type providerModelsRefreshResultMsg struct {
	vendor      string
	updated     int
	discovered  int
	skipped     int
	saveErr     error
	discoverErr error
}

const (
	providerPanelFocusVendor = iota
	providerPanelFocusEndpoint
	providerPanelFocusModel
)

func newProviderPanelFromConfig(cfg *ConfigView) *providerPanelState {
	vendorIDs := cfg.VendorNames()
	panel := &providerPanelState{
		vendorIDs:   vendorIDs,
		modelFilter: newModelFilterInput(LangEnglish),
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
	p.modelFilter.SetValue("")
	p.modelFilter.Blur()
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
	p.modelFilter.SetValue("")
	p.modelFilter.Blur()
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
	m.modelPanel = nil
	m.providerPanel = newProviderPanelFromConfig(m.configView())
	m.providerPanel.modelFilter = newModelFilterInput(m.currentLanguage())
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
	if providerHasUsableAPIKey(firstNonEmptyValue(ep.APIKey, vc.APIKey)) {
		apiKeyState = m.t("panel.provider.api_key.configured")
	} else {
		apiKeyState = m.t("panel.provider.api_key.missing")
	}
	baseURLState := ep.BaseURL
	if baseURLState == "" {
		baseURLState = m.t("panel.provider.base_url.not_set")
	}
	model := panel.selectedModel()
	if model == "" {
		model = m.t("panel.provider.model.set_with_m")
	}
	window := buildModelListWindow(panel.models, panel.modelIndex, panel.modelFilter)
	contentWidth := m.boxInnerWidth(m.mainColumnWidth())
	leftWidth := max(24, contentWidth*2/5)
	rightWidth := max(28, contentWidth-leftWidth-1)
	if leftWidth+rightWidth+1 > contentWidth {
		leftWidth = max(20, contentWidth-rightWidth-1)
	}
	if leftWidth < 1 {
		leftWidth = 1
	}
	if rightWidth < 1 {
		rightWidth = 1
	}

	columnHeight := providerPanelColumnsHeight(m.viewHeight())
	endpointHeight := max(5, columnHeight/3)
	modelHeight := max(8, columnHeight-endpointHeight-1)
	endpointHeight = columnHeight - modelHeight - 1
	footerHeight := providerPanelFooterHeight()

	leftColumn := renderProviderPanelSection(
		m.t("panel.provider.vendors"),
		m.renderProviderList(panel.vendorIDs, panel.vendorIndex, panel.focus == providerPanelFocusVendor),
		leftWidth,
		columnHeight,
	)

	rightTop := renderProviderPanelSection(
		m.t("panel.provider.endpoints"),
		m.renderProviderList(panel.endpointIDs, panel.endpointIndex, panel.focus == providerPanelFocusEndpoint),
		rightWidth,
		endpointHeight,
	)

	modelBody := []string{}
	if window.filterEnabled {
		modelBody = append(modelBody, panel.modelFilter.View())
	}
	modelBody = append(modelBody, renderModelListWindow(m.renderProviderList, window, panel.focus == providerPanelFocusModel, m.currentLanguage()))
	rightBottom := renderProviderPanelSection(
		m.t("panel.provider.models"),
		strings.Join(modelBody, "\n"),
		rightWidth,
		modelHeight,
	)

	columns := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftColumn,
		horizontalColumnGap(),
		lipgloss.JoinVertical(lipgloss.Left, rightTop, verticalSectionGap(), rightBottom),
	)

	footer := []string{
		fmt.Sprintf(" %s: %s / %s / %s", m.t("panel.provider.active_draft"), panel.selectedVendor(), panel.selectedEndpoint(), model),
		fmt.Sprintf(" %s: %s", m.t("panel.provider.protocol"), firstNonEmptyValue(ep.Protocol, m.t("panel.provider.protocol.unknown"))),
		fmt.Sprintf(" %s: %s", m.t("panel.provider.api_key"), apiKeyState),
		fmt.Sprintf(" %s: %s", m.t("panel.provider.base_url"), baseURLState),
		fmt.Sprintf(" %s: %s", m.t("panel.provider.tags"), strings.Join(ep.Tags, ", ")),
	}
	if panel.refreshing {
		footer = append(footer, lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(" "+m.t("panel.provider.refreshing_vendor", panel.refreshVendor)))
	}
	if panel.editingField != "" {
		footer = append(footer,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.edit")+" "+providerEditFieldLabel(m.currentLanguage(), panel.editingField)),
			panel.editInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	} else {
		footer = append(footer, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.main")))
	}
	if panel.message != "" {
		footer = append(footer, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}

	footerBox := lipgloss.NewStyle().
		Width(contentWidth).
		Height(footerHeight).
		MaxHeight(footerHeight).
		Render(strings.Join(footer, "\n"))

	return m.renderContextBox("/provider", lipgloss.JoinVertical(lipgloss.Left, columns, "", footerBox), lipgloss.Color("14"))
}

func renderProviderPanelSection(title, body string, width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 2 {
		height = 2
	}
	header := lipgloss.NewStyle().Bold(true).Render(" " + title)
	bodyStyle := lipgloss.NewStyle().
		Width(width).
		Height(height - 1).
		MaxHeight(height - 1)
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Render(header + "\n" + bodyStyle.Render(body))
}

func providerPanelColumnsHeight(viewHeight int) int {
	return min(28, max(16, viewHeight-12))
}

func providerPanelFooterHeight() int {
	return 8
}

func horizontalColumnGap() string {
	return " "
}

func verticalSectionGap() string {
	return "\n"
}

func (m *Model) renderProviderList(items []string, selected int, focused bool) string {
	if len(items) == 0 {
		return "  " + m.t("panel.model_list.none")
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
			panel.message = m.t("panel.provider.saved")
			panel.refreshing = false
			panel.refreshVendor = ""
			return *m, nil
		default:
			var cmd tea.Cmd
			panel.editInput, cmd = panel.editInput.Update(msg)
			return *m, cmd
		}
	}
	if panel.focus == providerPanelFocusModel && panel.modelFilter.Focused() && modelFilterConsumesKey(msg.String()) {
		var cmd tea.Cmd
		panel.modelFilter, cmd = panel.modelFilter.Update(msg)
		syncModelSelection(&panel.modelIndex, panel.models, panel.modelFilter)
		return *m, cmd
	}

	cfgView := m.configView()
	switch msg.String() {
	case "esc":
		if panel.modelFilter.Focused() {
			panel.modelFilter.Blur()
			return *m, nil
		}
		m.closeProviderPanel()
		return *m, nil
	case "/":
		if panel.focus == providerPanelFocusModel && shouldEnableModelFilter(panel.models) {
			panel.modelFilter.Focus()
			return *m, nil
		}
		return *m, nil
	case "tab", "right":
		panel.modelFilter.Blur()
		panel.focus = (panel.focus + 1) % 3
		return *m, nil
	case "shift+tab", "left":
		panel.modelFilter.Blur()
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
				return *m, m.refreshProviderModelsForVendor(panel.selectedVendor())
			}
		case providerPanelFocusEndpoint:
			if len(panel.endpointIDs) > 0 {
				panel.endpointIndex = (panel.endpointIndex - 1 + len(panel.endpointIDs)) % len(panel.endpointIDs)
				panel.modelIndex = 0
				panel.syncLists(cfgView)
			}
		case providerPanelFocusModel:
			if len(panel.models) > 0 {
				if panel.modelFilter.Focused() && msg.String() == "k" {
					break
				}
				moveFilteredModelSelection(&panel.modelIndex, panel.models, panel.modelFilter, -1)
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
				return *m, m.refreshProviderModelsForVendor(panel.selectedVendor())
			}
		case providerPanelFocusEndpoint:
			if len(panel.endpointIDs) > 0 {
				panel.endpointIndex = (panel.endpointIndex + 1) % len(panel.endpointIDs)
				panel.modelIndex = 0
				panel.syncLists(cfgView)
			}
		case providerPanelFocusModel:
			if len(panel.models) > 0 {
				if panel.modelFilter.Focused() && msg.String() == "j" {
					break
				}
				moveFilteredModelSelection(&panel.modelIndex, panel.models, panel.modelFilter, 1)
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
		if err := m.tryActivateCurrentSelection(); err != nil {
			panel.message = "Saved config, but current runtime is still inactive: " + err.Error()
			return *m, nil
		}
		m.syncSessionSelection()
		panel.message = m.t("panel.provider.saved_activated")
		return *m, nil
	}
	return *m, nil
}

func (m *Model) refreshProviderModelsForVendor(vendor string) tea.Cmd {
	if m.config == nil {
		return nil
	}
	vc, ok := m.config.Vendors[vendor]
	if !ok {
		return nil
	}

	refreshable := false
	for endpointID, endpoint := range vc.Endpoints {
		if !providerHasUsableAPIKey(firstNonEmptyValue(endpoint.APIKey, vc.APIKey)) {
			continue
		}
		if strings.TrimSpace(endpoint.BaseURL) == "" {
			continue
		}
		if endpoint.Protocol != "openai" && endpoint.Protocol != "anthropic" && endpoint.Protocol != "gemini" {
			continue
		}
		if _, err := m.config.ResolveEndpoint(vendor, endpointID); err == nil {
			refreshable = true
			break
		}
	}
	if !refreshable {
		return nil
	}

	if m.providerPanel != nil {
		m.providerPanel.refreshing = true
		m.providerPanel.refreshVendor = vendor
		m.providerPanel.message = m.t("panel.provider.refreshing_vendor", vendor)
	}

	return func() tea.Msg {
		result := providerModelsRefreshResultMsg{vendor: vendor}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		for endpointID, endpoint := range vc.Endpoints {
			if !providerHasUsableAPIKey(firstNonEmptyValue(endpoint.APIKey, vc.APIKey)) || strings.TrimSpace(endpoint.BaseURL) == "" {
				result.skipped++
				continue
			}
			if endpoint.Protocol != "openai" && endpoint.Protocol != "anthropic" && endpoint.Protocol != "gemini" {
				result.skipped++
				continue
			}

			resolved, err := m.config.ResolveEndpoint(vendor, endpointID)
			if err != nil {
				result.skipped++
				if result.discoverErr == nil {
					result.discoverErr = err
				}
				continue
			}

			models, err := provider.DiscoverModels(ctx, resolved)
			if err != nil {
				if result.discoverErr == nil {
					result.discoverErr = fmt.Errorf("%s: %w", endpointID, err)
				}
				continue
			}
			if err := m.config.SetEndpointModels(vendor, endpointID, models); err != nil {
				if result.discoverErr == nil {
					result.discoverErr = err
				}
				continue
			}
			result.updated++
			result.discovered += len(models)
		}

		if result.updated > 0 {
			if err := m.config.Save(); err != nil {
				result.saveErr = err
			}
		}
		return result
	}
}

func providerHasUsableAPIKey(value string) bool {
	value = strings.TrimSpace(config.ExpandEnv(value))
	if value == "" {
		return false
	}
	return !(strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}"))
}

func providerEditFieldLabel(lang Language, field string) string {
	switch field {
	case "vendor api key":
		return tr(lang, "panel.provider.edit.vendor_api_key")
	case "endpoint api key":
		return tr(lang, "panel.provider.edit.endpoint_api_key")
	case "endpoint base url":
		return tr(lang, "panel.provider.edit.endpoint_base_url")
	case "custom model":
		return tr(lang, "panel.provider.edit.custom_model")
	default:
		return field
	}
}
