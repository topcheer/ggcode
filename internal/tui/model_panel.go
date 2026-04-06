package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	)
	if panel.refreshing {
		body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Render(" "+m.t("panel.model.refreshing")))
	}
	body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+m.t("panel.model.hint.main")))
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/model", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) handleModelPanelKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	panel := m.modelPanel
	if panel == nil {
		return *m, nil
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
	case "r", "R":
		panel.filter.Blur()
		return *m, m.refreshActiveModelList()
	case "esc":
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
		if err := m.config.Save(); err != nil {
			panel.message = err.Error()
			return *m, nil
		}
		m.syncSessionSelection()
		if err := m.tryActivateCurrentSelection(); err != nil {
			panel.message = m.t("panel.model.saved_runtime_inactive", err.Error())
			return *m, nil
		}
		panel.message = m.t("panel.model.switched", model)
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
			result.saveErr = m.config.Save()
		} else {
			result.saveErr = err
		}
		return result
	}
}
