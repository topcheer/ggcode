package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/provider"
)

type modelPanelState struct {
	models     []string
	selected   int
	message    string
	refreshing bool
	remote     bool
	vendor     string
	endpoint   string
	protocol   string
	filter     textinput.Model

	// Inline editing for context_window (w) and max_tokens (o)
	editingField string // "context_window" or "max_tokens"
	editInput    textinput.Model
}

type modelPanelRefreshResultMsg struct {
	models      []string
	discoverErr error
	saveErr     error
	remote      bool
}

func (m *Model) openModelPanel() tea.Cmd {
	if m.config == nil {
		return nil
	}
	m.providerPanel = nil
	m.mcpPanel = nil
	m.skillsPanel = nil
	m.input.Blur() // release main input focus while panel is open

	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		return nil
	}
	models := uniqueStrings(append([]string(nil), resolved.Models...))
	if len(models) == 0 {
		models = []string{resolved.Model}
	}
	panel := &modelPanelState{
		models:   models,
		selected: indexOf(models, resolved.Model),
		vendor:   resolved.VendorName,
		endpoint: resolved.EndpointName,
		protocol: resolved.Protocol,
		filter:   newModelFilterInput(m.currentLanguage()),
	}
	if panel.selected < 0 {
		panel.selected = 0
	}
	m.modelPanel = panel
	return m.refreshActiveModelList()
}

func (m *Model) closeModelPanel() {
	m.modelPanel = nil
	m.input.Focus() // restore main input focus
}

func (m Model) renderModelPanel() string {
	panel := m.modelPanel
	if panel == nil || m.config == nil {
		return ""
	}
	source := "built-in"
	if panel.remote {
		source = m.t("panel.model.source.remote")
	} else {
		source = m.t("panel.model.source.builtin")
	}

	// Resolve current context_window and max_tokens for display.
	// Priority: session-level override → endpoint-level → auto
	cw, mt := "auto", "auto"
	if m.session != nil && m.session.ContextWindow > 0 {
		cw = fmt.Sprintf("%d", m.session.ContextWindow)
	} else if ep := m.config.ActiveEndpointConfig(); ep != nil && ep.ContextWindow > 0 {
		cw = fmt.Sprintf("%d", ep.ContextWindow)
	}
	if m.session != nil && m.session.MaxTokens > 0 {
		mt = fmt.Sprintf("%d", m.session.MaxTokens)
	} else if ep := m.config.ActiveEndpointConfig(); ep != nil && ep.MaxTokens > 0 {
		mt = fmt.Sprintf("%d", ep.MaxTokens)
	}

	window := buildModelListWindow(panel.models, panel.selected, panel.filter)
	body := []string{
		lipgloss.NewStyle().Bold(true).Render(" " + m.t("panel.model.models")),
	}
	if window.filterEnabled {
		body = append(body, panel.filter.View())
	}
	body = append(body,
		renderModelListWindow(m.renderProviderList, window, true, m.currentLanguage()),
		"",
		fmt.Sprintf(" %s: %s", m.t("panel.model.vendor"), panel.vendor),
		fmt.Sprintf(" %s: %s", m.t("panel.model.endpoint"), panel.endpoint),
		fmt.Sprintf(" %s: %s", m.t("panel.model.protocol"), panel.protocol),
		fmt.Sprintf(" %s: %s", m.t("panel.model.source"), source),
		fmt.Sprintf(" %s: %s  |  %s: %s", m.t("panel.model.context_window"), cw, m.t("panel.model.max_tokens"), mt),
	)
	if panel.refreshing {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(" "+m.t("panel.model.refreshing")))
	}
	if panel.editingField != "" {
		label := m.t("panel.model.context_window")
		if panel.editingField == "max_tokens" {
			label = m.t("panel.model.max_tokens")
		}
		body = append(body,
			lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true).Render(" "+m.t("panel.model.edit")+" "+label),
			panel.editInput.View(),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.model.hint.edit")),
		)
	} else {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.model.hint.main")))
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/model", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) handleModelPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.modelPanel
	if panel == nil {
		return *m, nil
	}
	// Handle inline editing mode
	if panel.editingField != "" {
		return m.handleModelPanelEditKey(msg)
	}
	if panel.filter.Focused() && modelFilterConsumesKey(msg.String()) {
		var cmd tea.Cmd
		panel.filter, cmd = panel.filter.Update(msg)
		syncModelSelection(&panel.selected, panel.models, panel.filter)
		return *m, cmd
	}
	switch msg.String() {
	case "/":
		if shouldEnableModelFilter(panel.models) {
			panel.filter.Focus()
			return *m, nil
		}
	case "up", "k":
		if len(panel.models) > 0 {
			if panel.filter.Focused() && msg.String() == "k" {
				break
			}
			moveFilteredModelSelection(&panel.selected, panel.models, panel.filter, -1)
		}
		return *m, nil
	case "down", "j", "tab":
		if len(panel.models) > 0 {
			if panel.filter.Focused() && msg.String() == "j" {
				break
			}
			moveFilteredModelSelection(&panel.selected, panel.models, panel.filter, 1)
		}
		return *m, nil
	case "shift+tab":
		if len(panel.models) > 0 {
			moveFilteredModelSelection(&panel.selected, panel.models, panel.filter, -1)
		}
		return *m, nil
	case "w":
		val := ""
		if m.session != nil && m.session.ContextWindow > 0 {
			val = fmt.Sprintf("%d", m.session.ContextWindow)
		} else if ep := m.config.ActiveEndpointConfig(); ep != nil && ep.ContextWindow > 0 {
			val = fmt.Sprintf("%d", ep.ContextWindow)
		}
		panel.startEditing("context_window", val)
		return *m, nil
	case "o":
		val := ""
		if m.session != nil && m.session.MaxTokens > 0 {
			val = fmt.Sprintf("%d", m.session.MaxTokens)
		} else if ep := m.config.ActiveEndpointConfig(); ep != nil && ep.MaxTokens > 0 {
			val = fmt.Sprintf("%d", ep.MaxTokens)
		}
		panel.startEditing("max_tokens", val)
		return *m, nil
	case "r", "R":
		panel.filter.Blur()
		return *m, m.refreshActiveModelList()
	case "esc", "ctrl+c":
		if panel.filter.Focused() {
			panel.filter.Blur()
			return *m, nil
		}
		m.closeModelPanel()
		return *m, nil
	case "enter", "s":
		if panel.filter.Focused() {
			panel.filter.Blur()
			return *m, nil
		}
		if len(panel.models) == 0 {
			return *m, nil
		}
		model := panel.models[panel.selected]
		if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, model); err != nil {
			panel.message = err.Error()
			return *m, nil
		}
		if err := m.tryActivateCurrentSelection(); err != nil {
			panel.message = m.t("panel.model.saved_runtime_inactive", err.Error())
			return *m, nil
		}
		m.syncSessionSelection()
		m.closeModelPanel()
		return *m, nil
	}
	return *m, nil
}

func (m *Model) refreshActiveModelList() tea.Cmd {
	if m.config == nil || m.modelPanel == nil {
		return nil
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		m.modelPanel.message = err.Error()
		return nil
	}
	m.modelPanel.refreshing = true
	m.modelPanel.message = ""
	m.modelPanel.filter.Blur()
	builtIn := uniqueStrings(append([]string(nil), resolved.Models...))
	if len(builtIn) == 0 {
		builtIn = []string{resolved.Model}
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()

		models, err := provider.DiscoverModels(ctx, resolved)
		if err != nil {
			return modelPanelRefreshResultMsg{
				models:      builtIn,
				discoverErr: err,
				remote:      false,
			}
		}

		result := modelPanelRefreshResultMsg{
			models: uniqueStrings(models),
			remote: true,
		}
		if err := m.config.SetEndpointModels(m.config.Vendor, m.config.Endpoint, result.models); err == nil {
			result.saveErr = m.saveConfig()
		} else {
			result.saveErr = err
		}
		return result
	}
}

// startEditing begins inline editing of a numeric field.
func (p *modelPanelState) startEditing(field, initialValue string) {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.SetValue(initialValue)
	ti.CursorEnd()
	ti.Focus()
	p.editingField = field
	p.editInput = ti
}

// handleModelPanelEditKey processes keys while in inline edit mode.
func (m *Model) handleModelPanelEditKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.modelPanel
	switch msg.String() {
	case "esc", "ctrl+c":
		panel.editingField = ""
		panel.editInput = textinput.Model{}
		m.input.Focus() // restore main input
		return *m, nil
	case "enter":
		val := strings.TrimSpace(panel.editInput.Value())
		field := panel.editingField // capture before clearing
		panel.editingField = ""
		panel.editInput = textinput.Model{}
		m.input.Focus() // restore main input

		vendor := m.config.Vendor
		endpoint := m.config.Endpoint
		vc, ok := m.config.Vendors[vendor]
		if !ok {
			panel.message = "vendor not found"
			return *m, nil
		}
		ep, ok := vc.Endpoints[endpoint]
		if !ok {
			panel.message = "endpoint not found"
			return *m, nil
		}

		var n int
		if val != "" {
			var parseErr error
			n, parseErr = parseIntPositive(val)
			if parseErr != nil {
				panel.message = parseErr.Error()
				return *m, nil
			}
		}

		// Persist to session-level (not endpoint config) so different
		// sessions can have different context_window/max_tokens.
		if m.session == nil {
			panel.message = m.t("panel.model.no_session")
			return *m, nil
		}

		switch field {
		case "context_window":
			m.session.ContextWindow = n
		case "max_tokens":
			m.session.MaxTokens = n
		}
		if m.sessionStore != nil {
			if err := m.sessionStore.AppendMetaToDisk(m.session); err != nil {
				panel.message = fmt.Sprintf("save failed: %v", err)
				return *m, nil
			}
		}

		// Apply to running agent immediately
		if m.agent != nil && m.agent.ContextManager() != nil {
			switch field {
			case "context_window":
				if n > 0 {
					m.agent.ContextManager().SetContextWindow(n)
				} else if resolved, _, err := agentruntime.ResolveCurrentSelection(m.config); err == nil {
					agentruntime.ApplyResolvedLimitsToAgent(m.agent, resolved)
				}
			case "max_tokens":
				if n > 0 {
					m.agent.ContextManager().SetOutputReserve(n)
				} else if resolved, _, err := agentruntime.ResolveCurrentSelection(m.config); err == nil {
					agentruntime.ApplyResolvedLimitsToAgent(m.agent, resolved)
				}
			}
		}

		// Display the effective values: session-level if set, otherwise endpoint-level
		displayCW := n
		displayMT := n
		if field == "context_window" {
			displayMT = m.session.MaxTokens
			if displayMT == 0 {
				displayMT = ep.MaxTokens
			}
		} else {
			displayCW = m.session.ContextWindow
			if displayCW == 0 {
				displayCW = ep.ContextWindow
			}
		}

		if val == "" || n == 0 {
			panel.message = m.t("panel.model.context_cleared")
		} else {
			panel.message = fmt.Sprintf(m.t("panel.model.context_applied"), displayCW, displayMT)
		}
		return *m, nil
	default:
		var cmd tea.Cmd
		panel.editInput, cmd = panel.editInput.Update(msg)
		return *m, cmd
	}
}

// parseIntPositive parses a non-negative integer string with optional K/M/G suffix.
// Examples: "256k" → 262144, "1M" → 1048576, "2G" → 2147483648, "128000" → 128000.
func parseIntPositive(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	upper := strings.ToUpper(s)
	mult := 1
	switch {
	case strings.HasSuffix(upper, "G"):
		mult = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	case strings.HasSuffix(upper, "M"):
		mult = 1024 * 1024
		s = s[:len(s)-1]
	case strings.HasSuffix(upper, "K"):
		mult = 1024
		s = s[:len(s)-1]
	}
	var val int
	_, err := fmt.Sscanf(s, "%d", &val)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %q", s)
	}
	if val < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	return val * mult, nil
}
