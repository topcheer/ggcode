package tui

import (
	"context"
	"fmt"
	"os"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/auth"
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
	authBusy      bool
	enterpriseURL string

	// New vendor creation wizard
	newVendorStep     int // 0=inactive, 1=vendor name, 2=endpoint name, 3=protocol, 4=base url, 5=api key
	newVendorInput    textinput.Model
	newVendorData     newVendorFormData
	newVendorProtoIdx int

	// New endpoint creation wizard (adds endpoint to existing vendor)
	// Steps: 1=endpoint name, 2=protocol, 3=base url, 4=api key
	newEndpointStep int
	newEndpointData newEndpointFormData
}

type newVendorFormData struct {
	vendorName   string
	endpointName string
	protocol     string
	baseURL      string
	apiKey       string
}

type newEndpointFormData struct {
	endpointName string
	protocol     string
	baseURL      string
	apiKey       string
}

var newVendorProtocols = []string{"openai", "anthropic", "gemini", "copilot"}

type providerModelsRefreshResultMsg struct {
	vendor      string
	updated     int
	discovered  int
	skipped     int
	saveErr     error
	discoverErr error
}

type providerAuthStartMsg struct {
	vendor     string
	flow       *auth.CopilotDeviceFlow
	claudeFlow *auth.ClaudeOAuthFlow
	copyErr    error
	openErr    error
	err        error
}

type providerAuthResultMsg struct {
	vendor string
	info   *auth.Info
	err    error
}

const providerPanelVisibleModelRows = 10

const (
	providerPanelFocusVendor = iota
	providerPanelFocusEndpoint
	providerPanelFocusModel
)

const (
	newVendorStepVendorName = iota + 1
	newVendorStepEndpointName
	newVendorStepProtocol
	newVendorStepBaseURL
	newVendorStepAPIKey
)

const (
	newEndpointStepEndpointName = iota + 1
	newEndpointStepProtocol
	newEndpointStepBaseURL
	newEndpointStepAPIKey
)

func newProviderPanelFromConfig(cfg *ConfigView) *providerPanelState {
	vendorIDs := cfg.VendorNames()
	panel := &providerPanelState{
		vendorIDs:   vendorIDs,
		modelFilter: newModelFilterInput(LangEnglish),
	}
	if info, err := auth.DefaultStore().Load(auth.ProviderGitHubCopilot); err == nil && info != nil {
		panel.enterpriseURL = info.EnterpriseURL
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

// reloadProviderConfigFromDisk merges vendor/endpoint definitions and API keys
// from disk into the live config so changes made by other ggcode instances
// (a new endpoint added elsewhere, an API key set elsewhere) become visible
// when this instance opens the provider panel.
//
// It performs a shallow merge: entries that already exist in memory are kept
// untouched (runtime may have updated models/selection), while vendor/endpoint
// definitions that exist on disk but not in memory are added. The current
// runtime selection (Vendor/Endpoint/Model) driven by the session is NEVER
// overwritten — we only reconcile the structural diff.
func (m *Model) reloadProviderConfigFromDisk() {
	if m.config == nil || m.config.FilePath == "" {
		return
	}
	// Only reload if a config file actually exists on disk. If the path is a
	// synthetic/in-memory path (e.g. tests or unsaved config), there is nothing
	// to reconcile against and we must keep the live config untouched.
	if _, err := os.Stat(m.config.FilePath); err != nil {
		return
	}
	workspace := m.currentWorkspacePath()
	fresh, err := config.LoadWithInstance(m.config.FilePath, workspace)
	if err != nil {
		debug.Log("config", "reloadProviderConfigFromDisk: load failed: %v", err)
		return
	}
	// API keys from keys.env were loaded into os.Setenv by LoadWithInstance
	// (via loadRuntimeEnv), so ${VAR} references now resolve for this process.

	// Shallow-merge vendor/endpoint definitions into the live config.
	for vendor, fvc := range fresh.Vendors {
		vc, exists := m.config.Vendors[vendor]
		if !exists {
			m.config.Vendors[vendor] = fvc
			continue
		}
		if vc.Endpoints == nil {
			vc.Endpoints = make(map[string]config.EndpointConfig)
		}
		for epName, fep := range fvc.Endpoints {
			if _, epExists := vc.Endpoints[epName]; !epExists {
				vc.Endpoints[epName] = fep
			}
		}
		// Pick up a vendor-level API key from disk if memory has none.
		if vc.APIKey == "" && fvc.APIKey != "" {
			vc.APIKey = fvc.APIKey
		}
		m.config.Vendors[vendor] = vc
	}
	debug.Log("config", "reloadProviderConfigFromDisk: merged vendor/endpoint defs from disk")
}

func (m *Model) openProviderPanel() {
	if m.config == nil {
		return
	}
	// Pull in vendor/endpoint defs and API keys authored by other instances.
	m.reloadProviderConfigFromDisk()
	m.modelPanel = nil
	m.providerPanel = newProviderPanelFromConfig(m.configView())
	m.providerPanel.modelFilter = newModelFilterInput(m.currentLanguage())
	m.input.Blur()
}

func (m *Model) closeProviderPanel() {
	m.providerPanel = nil
	m.input.Focus()
}

func (m *Model) renderProviderPanel() string {
	panel := m.providerPanel
	if panel == nil || m.config == nil {
		return ""
	}

	vc := m.config.Vendors[panel.selectedVendor()]
	ep := vc.Endpoints[panel.selectedEndpoint()]
	apiKeyState := providerCredentialStatus(m, panel.selectedVendor(), panel.selectedEndpoint(), ep, vc, panel)
	baseURLState := ep.BaseURL
	if baseURLState == "" {
		baseURLState = m.t("panel.provider.base_url.not_set")
	}
	if panel.selectedVendor() == auth.ProviderGitHubCopilot && panel.selectedEndpoint() == "enterprise" && strings.TrimSpace(panel.enterpriseURL) != "" {
		baseURLState = auth.CopilotAPIBaseURL(panel.enterpriseURL)
	}
	model := panel.selectedModel()
	if model == "" {
		model = m.t("panel.provider.model.set_with_m")
	}
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

	vendorHeight := providerPanelVendorHeight(len(panel.vendorIDs))
	endpointHeight := providerPanelEndpointHeight(len(panel.endpointIDs))
	modelFilterEnabled := providerPanelModelFilterEnabled(panel.models)
	modelHeight := providerPanelModelHeight(modelFilterEnabled)
	envVar := providerPanelAPIKeyEnvVar(panel.selectedVendor(), panel.selectedEndpoint(), vc, ep)
	wizardActive := panel.newVendorStep > 0 || panel.newEndpointStep > 0
	footerHeight := providerPanelFooterHeight(envVar != "")
	if wizardActive {
		footerHeight = 12 // give wizard enough room without info lines
	}

	leftColumn := renderProviderPanelSection(
		m.t("panel.provider.vendors"),
		renderProviderListWindow(m.renderProviderList, panel.vendorIDs, panel.vendorIndex, panel.focus == providerPanelFocusVendor, providerPanelVendorBodyRows(len(panel.vendorIDs))),
		leftWidth,
		vendorHeight,
	)

	rightTop := renderProviderPanelSection(
		m.t("panel.provider.endpoints"),
		renderProviderListWindow(m.renderProviderList, panel.endpointIDs, panel.endpointIndex, panel.focus == providerPanelFocusEndpoint, providerPanelEndpointBodyRows(len(panel.endpointIDs))),
		rightWidth,
		endpointHeight,
	)

	modelBody := []string{}
	if modelFilterEnabled {
		modelBody = append(modelBody, panel.modelFilter.View())
	}
	modelBody = append(modelBody, renderProviderModelListWindow(m.renderProviderList, panel.models, panel.modelIndex, panel.modelFilter, panel.focus == providerPanelFocusModel, m.currentLanguage()))
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

	var footer []string
	if !wizardActive {
		footer = []string{
			fmt.Sprintf(" %s: %s / %s / %s", m.t("panel.provider.active_draft"), panel.selectedVendor(), panel.selectedEndpoint(), model),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.protocol"), util.FirstNonEmpty(ep.Protocol, m.t("panel.provider.protocol.unknown"))),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.auth"), apiKeyState),
		}
		if envVar != "" {
			footer = append(footer, fmt.Sprintf(" %s: %s", m.t("panel.provider.env_var"), envVar))
		}
		footer = append(footer,
			fmt.Sprintf(" %s: %s", m.t("panel.provider.base_url"), baseURLState),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.tags"), strings.Join(ep.Tags, ", ")),
		)
		if panel.refreshing {
			footer = append(footer, lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(" "+m.t("panel.provider.refreshing_vendor", panel.refreshVendor)))
		}
	}
	if panel.editingField != "" {
		footer = append(footer,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.edit")+" "+providerEditFieldLabel(m.currentLanguage(), panel.editingField)),
			panel.editInput.View(),
			renderPasteShortcutHint(m.currentLanguage()),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	} else if panel.newVendorStep > 0 {
		footer = append(footer, m.renderNewVendorWizard()...)
	} else if panel.newEndpointStep > 0 {
		footer = append(footer, m.renderNewEndpointWizard()...)
	} else {
		footer = append(footer, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.main")))
	}
	if panel.selectedVendor() == auth.ProviderGitHubCopilot {
		footer = append(footer, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.copilot")))
	}
	if panel.selectedVendor() == auth.ProviderAnthropic && panel.selectedEndpoint() == "oauth" {
		footer = append(footer, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.anthropic_oauth")))
	}
	if panel.selectedVendor() == auth.ProviderGitHubCopilot && panel.selectedEndpoint() == "enterprise" && strings.TrimSpace(panel.enterpriseURL) != "" {
		footer = append(footer, fmt.Sprintf(" %s: %s", m.t("panel.provider.enterprise_url"), panel.enterpriseURL))
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

func providerPanelVendorBodyRows(vendorCount int) int {
	return providerPanelListBodyRows(vendorCount, 4, 24, 1) // doubled max from 12→24
}

func providerPanelVendorHeight(vendorCount int) int {
	return providerPanelVendorBodyRows(vendorCount) + 1 + 4
}

func providerPanelEndpointBodyRows(endpointCount int) int {
	return providerPanelListBodyRows(endpointCount, 3, 10, 2) // doubled max from 5→10
}

func providerPanelEndpointHeight(endpointCount int) int {
	return max(3, providerPanelEndpointBodyRows(endpointCount)+1-2)
}

func providerPanelModelHeight(filterEnabled bool) int {
	rows := providerPanelVisibleModelRows
	if filterEnabled {
		rows++
	}
	return rows + 1
}

func providerPanelListBodyRows(itemCount, minRows, maxRows, reserveRows int) int {
	rows := itemCount + reserveRows
	if rows < minRows {
		rows = minRows
	}
	if rows > maxRows {
		rows = maxRows
	}
	return rows
}

func renderProviderListWindow(renderList func([]string, int, bool) string, items []string, selected int, focused bool, maxRows int) string {
	if len(items) == 0 || maxRows <= 0 || len(items) <= maxRows {
		return renderList(items, selected, focused)
	}
	if selected < 0 || selected >= len(items) {
		selected = 0
	}
	start := selected - maxRows/2
	if start < 0 {
		start = 0
	}
	maxStart := len(items) - maxRows
	if start > maxStart {
		start = maxStart
	}
	end := start + maxRows
	windowItems := items[start:end]
	return renderList(windowItems, selected-start, focused)
}

func renderProviderModelListWindow(renderList func([]string, int, bool) string, models []string, selected int, filter textinput.Model, focused bool, lang Language) string {
	if len(models) == 0 {
		return "  " + tr(lang, "panel.model_list.none")
	}
	items, indices := filteredModelItems(models, filter.Value())
	if len(items) == 0 {
		return "  " + tr(lang, "panel.model_list.no_matches")
	}
	selectedPos := indexOfInt(indices, selected)
	if selectedPos < 0 {
		selectedPos = 0
	}
	return renderProviderListWindow(renderList, items, selectedPos, focused, providerPanelVisibleModelRows)
}

func providerPanelModelFilterEnabled(models []string) bool {
	return len(models) > providerPanelVisibleModelRows
}

func providerPanelFooterHeight(showEnvVar bool) int {
	if showEnvVar {
		return 9
	}
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

func (m *Model) handleProviderPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.providerPanel
	if panel == nil {
		return *m, nil
	}
	// Handle new endpoint wizard input
	if panel.newEndpointStep > 0 {
		switch msg.String() {
		case "esc", "ctrl+c":
			panel.newEndpointStep = 0
			panel.message = ""
			return *m, nil
		case "up":
			if panel.newEndpointStep == newEndpointStepProtocol && len(newVendorProtocols) > 0 {
				panel.newVendorProtoIdx = (panel.newVendorProtoIdx - 1 + len(newVendorProtocols)) % len(newVendorProtocols)
			}
			return *m, nil
		case "down":
			if panel.newEndpointStep == newEndpointStepProtocol && len(newVendorProtocols) > 0 {
				panel.newVendorProtoIdx = (panel.newVendorProtoIdx + 1) % len(newVendorProtocols)
			}
			return *m, nil
		case "enter":
			return m.handleNewEndpointStep()
		default:
			if panel.newEndpointStep != newEndpointStepProtocol {
				var cmd tea.Cmd
				panel.newVendorInput, cmd = panel.newVendorInput.Update(msg)
				return *m, cmd
			}
			return *m, nil
		}
	}

	// Handle new vendor wizard input
	if panel.newVendorStep > 0 {
		switch msg.String() {
		case "esc", "ctrl+c":
			panel.newVendorStep = 0
			panel.message = ""
			return *m, nil
		case "up":
			if panel.newVendorStep == newVendorStepProtocol && len(newVendorProtocols) > 0 {
				panel.newVendorProtoIdx = (panel.newVendorProtoIdx - 1 + len(newVendorProtocols)) % len(newVendorProtocols)
			}
			return *m, nil
		case "down":
			if panel.newVendorStep == newVendorStepProtocol && len(newVendorProtocols) > 0 {
				panel.newVendorProtoIdx = (panel.newVendorProtoIdx + 1) % len(newVendorProtocols)
			}
			return *m, nil
		case "enter":
			return m.handleNewVendorStep()
		default:
			// Route text input to the wizard's textinput field
			if panel.newVendorStep != newVendorStepProtocol {
				var cmd tea.Cmd
				panel.newVendorInput, cmd = panel.newVendorInput.Update(msg)
				return *m, cmd
			}
			return *m, nil
		}
	}
	if panel.editingField != "" {
		switch msg.String() {
		case "esc", "ctrl+c":
			panel.editingField = ""
			panel.message = ""
			return *m, nil
		case "enter":
			value := strings.TrimSpace(panel.editInput.Value())
			editedField := panel.editingField
			var err error
			switch editedField {
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
			case "enterprise url":
				normalized, normErr := auth.NormalizeEnterpriseURL(value)
				if normErr != nil {
					err = normErr
				} else {
					panel.enterpriseURL = normalized
				}
			case "new endpoint name":
				if value == "" {
					err = fmt.Errorf("endpoint name cannot be empty")
				} else {
					vc := m.config.Vendors[panel.selectedVendor()]
					if _, exists := vc.Endpoints[value]; exists {
						err = fmt.Errorf("endpoint %q already exists", value)
					} else {
						vc.Endpoints[value] = config.EndpointConfig{
							Protocol: "openai",
						}
						m.config.Vendors[panel.selectedVendor()] = vc
					}
				}
			}
			if err != nil {
				panel.message = err.Error()
				return *m, nil
			}
			if editedField == "enterprise url" {
				panel.editingField = ""
				panel.message = m.t("panel.provider.saved")
				return *m, nil
			}
			if err := m.saveConfig(); err != nil {
				panel.message = err.Error()
				return *m, nil
			}
			// If the user just saved an API key, rebuild the provider
			// immediately so the new key takes effect.
			if editedField == "vendor api key" || editedField == "endpoint api key" {
				m.ensureProviderSync()
			}
			panel.editingField = ""
			panel.message = m.t("panel.provider.saved")
			panel.refreshing = false
			panel.refreshVendor = ""
			if editedField == "new endpoint name" {
				panel.endpointIDs = m.config.EndpointNames(panel.selectedVendor())
				epName := strings.TrimSpace(panel.editInput.Value())
				panel.endpointIndex = indexOf(panel.endpointIDs, epName)
				if panel.endpointIndex < 0 {
					panel.endpointIndex = 0
				}
				panel.modelIndex = 0
				panel.models = nil
			}
			if providerEditShouldRefreshModels(editedField) {
				if cmd := m.refreshProviderModelsForVendor(panel.selectedVendor()); cmd != nil {
					return *m, cmd
				}
			}
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
	case "esc", "ctrl+c":
		if panel.modelFilter.Focused() {
			panel.modelFilter.Blur()
			return *m, nil
		}
		m.closeProviderPanel()
		return *m, nil
	case "/":
		if panel.focus == providerPanelFocusModel && providerPanelModelFilterEnabled(panel.models) {
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
				return *m, m.refreshProviderModelsForVendor(panel.selectedVendor())
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
				return *m, m.refreshProviderModelsForVendor(panel.selectedVendor())
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
		if panel.selectedVendor() == auth.ProviderGitHubCopilot && panel.selectedEndpoint() == "enterprise" {
			panel.startEditing("enterprise url", panel.enterpriseURL)
			return *m, nil
		}
		vc := m.config.Vendors[panel.selectedVendor()]
		ep := vc.Endpoints[panel.selectedEndpoint()]
		panel.startEditing("endpoint base url", ep.BaseURL)
		return *m, nil
	case "e":
		// Start new endpoint creation wizard for the current vendor
		panel.newEndpointStep = newEndpointStepEndpointName
		panel.newEndpointData = newEndpointFormData{}
		panel.newVendorProtoIdx = 0
		ti := textinput.New()
		ti.Prompt = "❯ "
		ti.Focus()
		panel.newVendorInput = ti
		panel.message = ""
		return *m, nil
	case "m":
		panel.startEditing("custom model", panel.selectedModel())
		return *m, nil
	case "l":
		if panel.authBusy {
			return *m, nil
		}
		if panel.selectedVendor() == auth.ProviderAnthropic && panel.selectedEndpoint() == "oauth" {
			panel.authBusy = true
			panel.message = m.t("panel.provider.login.claude_starting")
			return *m, m.startClaudeLogin()
		}
		if panel.selectedVendor() == auth.ProviderGitHubCopilot {
			panel.authBusy = true
			panel.message = m.t("panel.provider.login.starting")
			return *m, m.startCopilotLogin(panel.selectedEndpoint(), panel.enterpriseURL)
		}
		return *m, nil
	case "x":
		if panel.authBusy {
			return *m, nil
		}
		if panel.selectedVendor() == auth.ProviderAnthropic && panel.selectedEndpoint() == "oauth" {
			if err := auth.DefaultStore().Delete(auth.ProviderAnthropic); err != nil {
				panel.message = err.Error()
				return *m, nil
			}
			panel.message = m.t("panel.provider.logout.claude_success")
			return *m, nil
		}
		if panel.selectedVendor() == auth.ProviderGitHubCopilot {
			if err := auth.DefaultStore().Delete(auth.ProviderGitHubCopilot); err != nil {
				panel.message = err.Error()
				return *m, nil
			}
			panel.message = m.t("panel.provider.logout.success")
			return *m, nil
		}
		return *m, nil
	case "n":
		// Start new vendor creation wizard
		panel.newVendorStep = newVendorStepVendorName
		panel.newVendorData = newVendorFormData{}
		panel.newVendorProtoIdx = 0
		ti := textinput.New()
		ti.Prompt = "❯ "
		ti.Focus()
		panel.newVendorInput = ti
		panel.message = ""
		return *m, nil
	case "enter", "s":
		// If new vendor wizard is active, handle step progression
		if panel.newVendorStep > 0 {
			return m.handleNewVendorStep()
		}
		if err := m.config.SetActiveSelection(panel.selectedVendor(), panel.selectedEndpoint(), panel.selectedModel()); err != nil {
			panel.message = err.Error()
			return *m, nil
		}
		if err := m.tryActivateCurrentSelection(); err != nil {
			panel.message = m.t("panel.model.saved_runtime_inactive", err)
			return *m, nil
		}
		m.syncSessionSelection()
		panel.message = m.t("panel.provider.saved_activated")
		return *m, nil
	}
	return *m, nil
}

// handleNewVendorStep processes the current wizard step on Enter.
func (m *Model) handleNewVendorStep() (Model, tea.Cmd) {
	panel := m.providerPanel
	value := strings.TrimSpace(panel.newVendorInput.Value())
	step := panel.newVendorStep

	switch step {
	case newVendorStepVendorName:
		if value == "" {
			panel.message = m.t("panel.provider.new_vendor.error.name_empty")
			return *m, nil
		}
		if _, exists := m.config.Vendors[value]; exists {
			panel.message = m.t("panel.provider.new_vendor.error.vendor_exists")
			return *m, nil
		}
		panel.newVendorData.vendorName = value
		panel.newVendorStep = newVendorStepEndpointName
		ti := textinput.New()
		ti.Prompt = "❯ "
		ti.Focus()
		panel.newVendorInput = ti
		panel.message = ""
		return *m, nil

	case newVendorStepEndpointName:
		if value == "" {
			panel.message = m.t("panel.provider.new_vendor.error.endpoint_empty")
			return *m, nil
		}
		panel.newVendorData.endpointName = value
		panel.newVendorStep = newVendorStepProtocol
		panel.message = ""
		return *m, nil

	case newVendorStepProtocol:
		panel.newVendorData.protocol = newVendorProtocols[panel.newVendorProtoIdx]
		panel.newVendorStep = newVendorStepBaseURL
		ti := textinput.New()
		ti.Prompt = "❯ "
		ti.Focus()
		panel.newVendorInput = ti
		panel.message = ""
		return *m, nil

	case newVendorStepBaseURL:
		panel.newVendorData.baseURL = value
		panel.newVendorStep = newVendorStepAPIKey
		ti := textinput.New()
		ti.Prompt = "❯ "
		ti.Focus()
		panel.newVendorInput = ti
		panel.message = ""
		return *m, nil

	case newVendorStepAPIKey:
		panel.newVendorData.apiKey = value
		// Create the vendor and endpoint
		d := panel.newVendorData
		if err := m.config.AddVendor(d.vendorName, "", ""); err != nil {
			panel.message = err.Error()
			panel.newVendorStep = 0
			return *m, nil
		}
		if err := m.config.AddEndpoint(d.vendorName, d.endpointName, d.protocol, d.baseURL, d.apiKey); err != nil {
			panel.message = err.Error()
			panel.newVendorStep = 0
			return *m, nil
		}
		if err := m.saveConfig(); err != nil {
			panel.message = err.Error()
			panel.newVendorStep = 0
			return *m, nil
		}
		// Select the new vendor+endpoint
		panel.vendorIDs = m.config.VendorNames()
		panel.vendorIndex = indexOf(panel.vendorIDs, d.vendorName)
		if panel.vendorIndex < 0 {
			panel.vendorIndex = 0
		}
		panel.endpointIDs = m.config.EndpointNames(d.vendorName)
		panel.endpointIndex = indexOf(panel.endpointIDs, d.endpointName)
		if panel.endpointIndex < 0 {
			panel.endpointIndex = 0
		}
		panel.models = nil
		panel.modelIndex = 0
		panel.newVendorStep = 0
		panel.message = m.t("panel.provider.new_vendor.created", d.vendorName, d.endpointName)
		// Auto-discover models if base URL is present (API key optional for local endpoints like Ollama)
		if d.baseURL != "" {
			return *m, m.refreshProviderModelsForVendor(d.vendorName)
		}
		return *m, nil
	}
	panel.newVendorStep = 0
	return *m, nil
}

// handleNewEndpointStep processes the current new-endpoint wizard step on Enter.
func (m *Model) handleNewEndpointStep() (Model, tea.Cmd) {
	panel := m.providerPanel
	value := strings.TrimSpace(panel.newVendorInput.Value())
	step := panel.newEndpointStep

	switch step {
	case newEndpointStepEndpointName:
		if value == "" {
			panel.message = m.t("panel.provider.new_vendor.error.endpoint_empty")
			return *m, nil
		}
		vc := m.config.Vendors[panel.selectedVendor()]
		if _, exists := vc.Endpoints[value]; exists {
			panel.message = m.t("panel.provider.new_endpoint.error.endpoint_exists")
			return *m, nil
		}
		panel.newEndpointData.endpointName = value
		panel.newEndpointStep = newEndpointStepProtocol
		panel.message = ""
		return *m, nil

	case newEndpointStepProtocol:
		panel.newEndpointData.protocol = newVendorProtocols[panel.newVendorProtoIdx]
		panel.newEndpointStep = newEndpointStepBaseURL
		ti := textinput.New()
		ti.Prompt = "❯ "
		ti.Focus()
		panel.newVendorInput = ti
		panel.message = ""
		return *m, nil

	case newEndpointStepBaseURL:
		panel.newEndpointData.baseURL = value
		panel.newEndpointStep = newEndpointStepAPIKey
		ti := textinput.New()
		ti.Prompt = "❯ "
		ti.Focus()
		panel.newVendorInput = ti
		panel.message = ""
		return *m, nil

	case newEndpointStepAPIKey:
		panel.newEndpointData.apiKey = value
		d := panel.newEndpointData
		if err := m.config.AddEndpoint(panel.selectedVendor(), d.endpointName, d.protocol, d.baseURL, d.apiKey); err != nil {
			panel.message = err.Error()
			panel.newEndpointStep = 0
			return *m, nil
		}
		if err := m.saveConfig(); err != nil {
			panel.message = err.Error()
			panel.newEndpointStep = 0
			return *m, nil
		}
		panel.endpointIDs = m.config.EndpointNames(panel.selectedVendor())
		panel.endpointIndex = indexOf(panel.endpointIDs, d.endpointName)
		if panel.endpointIndex < 0 {
			panel.endpointIndex = 0
		}
		panel.models = nil
		panel.modelIndex = 0
		panel.newEndpointStep = 0
		panel.message = m.t("panel.provider.new_endpoint.created", d.endpointName)
		if d.baseURL != "" {
			return *m, m.refreshProviderModelsForVendor(panel.selectedVendor())
		}
		return *m, nil
	}
	panel.newEndpointStep = 0
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

	// Only refresh the currently selected endpoint, not all endpoints.
	endpointID := ""
	if m.providerPanel != nil {
		endpointID = m.providerPanel.selectedEndpoint()
	}
	if endpointID == "" {
		endpointID = m.config.Endpoint
	}
	endpoint, ok := vc.Endpoints[endpointID]
	if !ok {
		return nil
	}

	// Skip API key requirement for local endpoints (e.g., Ollama on localhost)
	isLocal := isLocalBaseURL(endpoint.BaseURL)
	if !isLocal {
		// AI Gateway vendors should not use vendor-level API key — each endpoint
		// must have its own key.
		isGateway := vendor == "ai-gateway"
		if !providerHasUsableCredential(vendor, endpointID, endpoint, vc, m.providerPanel) {
			// For gateway vendors, don't fall back to vendor-level key
			if isGateway || !providerHasUsableAPIKey(resolveAPIKey(endpoint.APIKey, "")) {
				return nil
			}
		}
	}
	if strings.TrimSpace(endpoint.BaseURL) == "" {
		return nil
	}
	if endpoint.Protocol != "openai" && endpoint.Protocol != "anthropic" && endpoint.Protocol != "gemini" && endpoint.Protocol != "copilot" {
		return nil
	}
	// Note: we intentionally do NOT call ResolveEndpoint here because it
	// requires a model to be set, and we're discovering models precisely
	// because there may not be one yet. The checks above (vendor/endpoint
	// existence, API key, baseURL, protocol) are sufficient.

	if m.providerPanel != nil {
		m.providerPanel.refreshing = true
		m.providerPanel.refreshVendor = vendor
		m.providerPanel.message = m.t("panel.provider.refreshing_vendor", vendor)
	}

	return func() tea.Msg {
		result := providerModelsRefreshResultMsg{vendor: vendor}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		// Try ResolveEndpoint first; if it fails (e.g. no model set yet),
		// construct ResolvedEndpoint manually from config for discovery.
		resolved, err := m.config.ResolveEndpoint(vendor, endpointID)
		if err != nil {
			resolved = &config.ResolvedEndpoint{
				VendorID:   vendor,
				EndpointID: endpointID,
				Protocol:   endpoint.Protocol,
				BaseURL:    endpoint.BaseURL,
				// Expand ${VAR} references so the actual key value (set via
				// os.Setenv when the user saved it) is used for discovery,
				// not the raw "${...}" reference string.
				APIKey: config.ExpandEnv(resolveAPIKey(endpoint.APIKey, vc.APIKey)),
			}
		}

		models, err := provider.DiscoverModels(ctx, resolved)
		if err != nil {
			result.skipped++
			result.discoverErr = fmt.Errorf("%s: %w", endpointID, err)
			return result
		}
		if err := m.config.SetEndpointModels(vendor, endpointID, models); err != nil {
			result.discoverErr = err
			return result
		}
		result.updated++
		result.discovered += len(models)

		if result.updated > 0 {
			if err := m.saveConfig(); err != nil {
				result.saveErr = err
			}
		}
		return result
	}
}

func (m *Model) startCopilotLogin(endpoint, enterpriseURL string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if endpoint != "enterprise" {
			enterpriseURL = ""
		}
		flow, err := auth.StartCopilotDeviceFlow(ctx, enterpriseURL)
		msg := providerAuthStartMsg{vendor: auth.ProviderGitHubCopilot, flow: flow, err: err}
		if err == nil && flow != nil {
			if m.clipboardWriter != nil {
				msg.copyErr = m.clipboardWriter(flow.UserCode)
			}
			if m.urlOpener != nil {
				msg.openErr = m.urlOpener(flow.VerificationURI)
			}
		}
		return msg
	}
}

func (m *Model) pollCopilotLogin(flow *auth.CopilotDeviceFlow) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		info, err := auth.PollCopilotDeviceFlow(ctx, flow)
		if err == nil && info != nil {
			err = auth.DefaultStore().Save(info)
		}
		return providerAuthResultMsg{vendor: auth.ProviderGitHubCopilot, info: info, err: err}
	}
}

func (m *Model) startClaudeLogin() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		flow, err := auth.StartClaudeOAuthFlow(ctx)
		msg := providerAuthStartMsg{vendor: auth.ProviderAnthropic, claudeFlow: flow, err: err}
		if err == nil && flow != nil {
			if m.urlOpener != nil {
				msg.openErr = m.urlOpener(flow.AutoURL)
			}
		}
		return msg
	}
}

func (m *Model) waitForClaudeAuthCode(flow *auth.ClaudeOAuthFlow) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		code, isAutomatic, err := auth.WaitForClaudeAuthCode(ctx, flow)
		flow.Close()
		if err != nil {
			return providerAuthResultMsg{vendor: auth.ProviderAnthropic, err: err}
		}
		// Exchange code for tokens
		tokenResp, exchangeErr := auth.ExchangeClaudeCodeForTokens(ctx, code, flow.CodeVerifier, !isAutomatic, flow.Port)
		if exchangeErr != nil {
			return providerAuthResultMsg{vendor: auth.ProviderAnthropic, err: exchangeErr}
		}
		expiresIn := tokenResp.ExpiresIn
		if expiresIn <= 0 {
			expiresIn = 3600
		}
		info := &auth.Info{
			ProviderID:   auth.ProviderAnthropic,
			Type:         "oauth",
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
		}
		if saveErr := auth.DefaultStore().Save(info); saveErr != nil {
			return providerAuthResultMsg{vendor: auth.ProviderAnthropic, err: saveErr}
		}
		return providerAuthResultMsg{vendor: auth.ProviderAnthropic, info: info}
	}
}

func providerHasUsableAPIKey(value string) bool {
	value = strings.TrimSpace(config.ExpandEnv(value))
	if value == "" {
		return false
	}
	return !(strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}"))
}

// isLocalBaseURL returns true for localhost/127.0.0.1 base URLs that don't
// require an API key (e.g., Ollama, LM Studio, vLLM local deployments).
func isLocalBaseURL(baseURL string) bool {
	u := strings.TrimSpace(baseURL)
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	host := u
	if i := strings.IndexByte(u, ':'); i >= 0 {
		host = u[:i]
	}
	if i := strings.IndexByte(u, '/'); i >= 0 && i < len(host) {
		host = host[:i]
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]"
}

func providerHasUsableCredential(vendor, endpoint string, ep config.EndpointConfig, vc config.VendorConfig, panel *providerPanelState) bool {
	if vendor == auth.ProviderAnthropic && endpoint == "oauth" {
		info, err := auth.DefaultStore().Load(auth.ProviderAnthropic)
		if err != nil || info == nil || strings.TrimSpace(info.AccessToken) == "" {
			return false
		}
		return true
	}
	if vendor == auth.ProviderGitHubCopilot {
		info, err := auth.DefaultStore().Load(auth.ProviderGitHubCopilot)
		if err != nil || info == nil || strings.TrimSpace(info.AccessToken) == "" {
			return false
		}
		if endpoint == "enterprise" {
			enterpriseURL := strings.TrimSpace(info.EnterpriseURL)
			if panel != nil && strings.TrimSpace(panel.enterpriseURL) != "" {
				enterpriseURL = strings.TrimSpace(panel.enterpriseURL)
			}
			return enterpriseURL != ""
		}
		return true
	}
	return providerHasUsableAPIKey(resolveAPIKey(ep.APIKey, vc.APIKey))
}

// resolveAPIKey picks the effective API key: endpoint key first,
// but falls back to vendor key if the endpoint key is an unresolvable ${VAR} reference.
func resolveAPIKey(epKey, vcKey string) string {
	key := util.FirstNonEmpty(epKey, vcKey)
	if !providerHasUsableAPIKey(key) && vcKey != "" {
		key = vcKey
	}
	return key
}

func providerCredentialStatus(m *Model, vendor, endpoint string, ep config.EndpointConfig, vc config.VendorConfig, panel *providerPanelState) string {
	if vendor == auth.ProviderAnthropic && endpoint == "oauth" {
		if providerHasUsableCredential(vendor, endpoint, ep, vc, panel) {
			return m.t("panel.provider.auth.connected")
		}
		return m.t("panel.provider.auth.not_connected")
	}
	if vendor == auth.ProviderGitHubCopilot {
		if providerHasUsableCredential(vendor, endpoint, ep, vc, panel) {
			return m.t("panel.provider.auth.connected")
		}
		return m.t("panel.provider.auth.not_connected")
	}
	// Try endpoint key first; if it's an unresolvable ${VAR} reference,
	// fall back to vendor key — the user may have set only the vendor key.
	key := resolveAPIKey(ep.APIKey, vc.APIKey)
	if providerHasUsableAPIKey(key) {
		return m.t("panel.provider.api_key.configured")
	}
	return m.t("panel.provider.api_key.missing")
}

func providerPanelAPIKeyEnvVar(vendor, endpoint string, vc config.VendorConfig, ep config.EndpointConfig) string {
	if vendor == auth.ProviderGitHubCopilot {
		return ""
	}
	return config.PreferredAPIKeyEnvVar(vendor, endpoint, vc.APIKey, ep.APIKey)
}

func providerEditShouldRefreshModels(field string) bool {
	switch field {
	case "endpoint api key", "endpoint base url":
		return true
	default:
		return false
	}
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
	case "new endpoint name":
		return tr(lang, "panel.provider.edit.new_endpoint_name")
	case "enterprise url":
		return tr(lang, "panel.provider.enterprise_url")
	default:
		return field
	}
}

// renderNewVendorWizard renders the multi-step new vendor creation wizard footer.
func (m *Model) renderNewVendorWizard() []string {
	panel := m.providerPanel
	lang := m.currentLanguage()
	var lines []string

	switch panel.newVendorStep {
	case newVendorStepVendorName:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_vendor.title")),
			m.t("panel.provider.new_vendor.step_vendor"),
			panel.newVendorInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	case newVendorStepEndpointName:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_vendor.title")),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.vendor"), panel.newVendorData.vendorName),
			m.t("panel.provider.new_vendor.step_endpoint"),
			panel.newVendorInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	case newVendorStepProtocol:
		protoLines := make([]string, 0, len(newVendorProtocols))
		for i, p := range newVendorProtocols {
			prefix := "  "
			style := lipgloss.NewStyle()
			if i == panel.newVendorProtoIdx {
				prefix = "❯ "
				style = style.Foreground(lipgloss.Color("226")).Background(lipgloss.Color("236"))
			}
			protoLines = append(protoLines, style.Render(prefix+p))
		}
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_vendor.title")),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.vendor"), panel.newVendorData.vendorName),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.endpoint"), panel.newVendorData.endpointName),
			m.t("panel.provider.new_vendor.step_protocol"),
			strings.Join(protoLines, "\n"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+tr(lang, "panel.provider.hint.protocol_select")),
		)
	case newVendorStepBaseURL:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_vendor.title")),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.vendor"), panel.newVendorData.vendorName),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.endpoint"), panel.newVendorData.endpointName),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.protocol"), panel.newVendorData.protocol),
			m.t("panel.provider.new_vendor.step_base_url"),
			panel.newVendorInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	case newVendorStepAPIKey:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_vendor.title")),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.vendor"), panel.newVendorData.vendorName),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.endpoint"), panel.newVendorData.endpointName),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.protocol"), panel.newVendorData.protocol),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.base_url"), panel.newVendorData.baseURL),
			m.t("panel.provider.new_vendor.step_api_key"),
			panel.newVendorInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	}
	if panel.message != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return lines
}

// renderNewEndpointWizard renders the multi-step new endpoint creation wizard footer.
func (m *Model) renderNewEndpointWizard() []string {
	panel := m.providerPanel
	lang := m.currentLanguage()
	var lines []string

	switch panel.newEndpointStep {
	case newEndpointStepEndpointName:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_endpoint.title")),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.vendor"), panel.selectedVendor()),
			m.t("panel.provider.new_vendor.step_endpoint"),
			panel.newVendorInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	case newEndpointStepProtocol:
		protoLines := make([]string, 0, len(newVendorProtocols))
		for i, p := range newVendorProtocols {
			prefix := "  "
			style := lipgloss.NewStyle()
			if i == panel.newVendorProtoIdx {
				prefix = "❯ "
				style = style.Foreground(lipgloss.Color("226")).Background(lipgloss.Color("236"))
			}
			protoLines = append(protoLines, style.Render(prefix+p))
		}
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_endpoint.title")),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.vendor"), panel.selectedVendor()),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.endpoint"), panel.newEndpointData.endpointName),
			m.t("panel.provider.new_vendor.step_protocol"),
			strings.Join(protoLines, "\n"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+tr(lang, "panel.provider.hint.protocol_select")),
		)
	case newEndpointStepBaseURL:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_endpoint.title")),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.vendor"), panel.selectedVendor()),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.endpoint"), panel.newEndpointData.endpointName),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.protocol"), panel.newEndpointData.protocol),
			m.t("panel.provider.new_vendor.step_base_url"),
			panel.newVendorInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	case newEndpointStepAPIKey:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.provider.new_endpoint.title")),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.vendor"), panel.selectedVendor()),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.endpoint"), panel.newEndpointData.endpointName),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.protocol"), panel.newEndpointData.protocol),
			fmt.Sprintf(" %s: %s", m.t("panel.provider.new_vendor.base_url"), panel.newEndpointData.baseURL),
			m.t("panel.provider.new_vendor.step_api_key"),
			panel.newVendorInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.provider.hint.edit")),
		)
	}
	if panel.message != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return lines
}
