package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/im"
)

type wecomPanelState struct {
	selected  int
	message   string
	editState imAdapterEditState
}

type wecomBindingEntry struct {
	Adapter      string
	OccupiedBy   string
	AdapterState *im.AdapterState
	Bound        bool
}

func (m *Model) openWeComPanel() {
	m.wecomPanel = &wecomPanelState{}
}

func (m *Model) closeWeComPanel() {
	m.wecomPanel = nil
}

func (m Model) renderWeComPanel() string {
	panel := m.wecomPanel
	if panel == nil {
		return ""
	}

	entries := m.wecomBindingEntries()

	var body []string

	// Header
	body = append(body,
		lipgloss.NewStyle().Bold(true).Render("WeCom (Enterprise WeChat)"),
		"",
	)

	// Adapter list
	if len(entries) == 0 {
		body = append(body, lipgloss.NewStyle().Faint(true).Render("No WeCom adapters configured."))
		body = append(body, "", "Add an adapter in ggcode.yaml:", "",
			"  im:", "    enabled: true",
			"    adapters:", "      my-wecom:",
			"        enabled: true",
			"        platform: wecom",
			"        extra:",
			"          bot_id: YOUR_BOT_ID",
			"          secret: YOUR_SECRET",
		)
	} else {
		selected := panel.selected
		if selected >= len(entries) {
			selected = len(entries) - 1
		}
		if selected < 0 {
			selected = 0
		}

		for i, entry := range entries {
			icon := "○"
			if entry.AdapterState != nil && entry.AdapterState.Healthy {
				icon = "●"
			}
			prefix := " "
			if i == selected {
				prefix = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Render("▸")
			}
			mark := " "
			if entry.Bound {
				mark = "*"
			}
			if entry.OccupiedBy != "" && !entry.Bound {
				mark = "⊕"
			}
			line := fmt.Sprintf("%s%s %s %s", prefix, mark, icon, entry.Adapter)
			body = append(body, line)
		}

		// Detail of selected entry
		if selected >= 0 && selected < len(entries) {
			entry := entries[selected]
			body = append(body, "", lipgloss.NewStyle().Bold(true).Render("Details"))

			var statusStr string
			switch {
			case entry.AdapterState == nil:
				statusStr = "offline"
			case entry.AdapterState.Healthy:
				statusStr = "connected"
			case entry.AdapterState.Status == "error":
				statusStr = fmt.Sprintf("error: %s", entry.AdapterState.LastError)
			default:
				statusStr = entry.AdapterState.Status
			}
			body = append(body, fmt.Sprintf("  Adapter: %s", entry.Adapter))
			body = append(body, fmt.Sprintf("  Status:  %s", statusStr))
			if entry.Bound {
				body = append(body, "  Bound:   this workspace")
			} else if entry.OccupiedBy != "" {
				body = append(body, fmt.Sprintf("  Bound:   %s", filepath.Base(entry.OccupiedBy)))
			}
		}
	}

	// Edit mode
	if panel.editState.mode == imEditSelect {
		body = append(body, "", m.renderIMEditSelect(&panel.editState))
	} else if panel.editState.mode == imEditInput {
		body = append(body, "", m.renderIMEditInput(&panel.editState))
	}

	// Help
	help := "[b] bind  [u] unbind  [e] edit  [d] delete  [↑↓] navigate  [esc] close"
	body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(help))

	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}

	return m.renderContextBox("/wecom", strings.Join(body, "\n"), lipgloss.Color("10"))
}

func (m *Model) handleWeComPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.wecomPanel
	if panel == nil {
		return *m, nil
	}

	// Edit mode intercepts all keys
	if panel.editState.mode != imEditNone {
		newState, cmd := m.handleIMEditKey(&panel.editState, msg)
		panel.editState = *newState
		return *m, cmd
	}

	entries := m.wecomBindingEntries()

	switch msg.String() {
	case "esc", "q":
		m.closeWeComPanel()
		return *m, nil
	case "up", "k":
		if len(entries) > 0 {
			panel.selected = (panel.selected - 1 + len(entries)) % len(entries)
		}
		panel.message = ""
	case "down", "j":
		if len(entries) > 0 {
			panel.selected = (panel.selected + 1) % len(entries)
		}
		panel.message = ""
	case "enter", "b":
		return m.wecomBindEntry(entries, panel)
	case "u":
		if len(entries) == 0 {
			return *m, nil
		}
		selected := panel.selected
		if selected >= len(entries) {
			selected = len(entries) - 1
		}
		return m.wecomUnbindEntry(entries[selected].Adapter, panel)
	case "e":
		if len(entries) == 0 {
			panel.message = "No adapters to edit"
			return *m, nil
		}
		selected := panel.selected
		if selected >= len(entries) {
			selected = len(entries) - 1
		}
		entry := entries[selected]
		panel.editState = m.enterIMEditSelect(entry.Adapter)
		panel.message = ""
	case "d":
		if len(entries) == 0 {
			return *m, nil
		}
		selected := panel.selected
		if selected >= len(entries) {
			selected = len(entries) - 1
		}
		entry := entries[selected]
		return *m, m.removeWeComEntry(entry)
	case "r":
		panel.message = "Refreshed"
	}
	return *m, nil
}

func (m *Model) wecomBindEntry(entries []wecomBindingEntry, panel *wecomPanelState) (Model, tea.Cmd) {
	if len(entries) == 0 {
		panel.message = "No adapters available"
		return *m, nil
	}
	selected := panel.selected
	if selected >= len(entries) {
		selected = len(entries) - 1
	}
	entry := entries[selected]
	ws := m.currentWorkspacePath()
	if ws == "" {
		panel.message = "No workspace path"
		return *m, nil
	}
	if m.imManager == nil {
		panel.message = "IM manager not available"
		return *m, nil
	}
	_, err := m.imManager.BindChannel(im.ChannelBinding{
		Workspace: ws,
		Platform:  im.PlatformWeCom,
		Adapter:   entry.Adapter,
		TargetID:  defaultWeComTargetID(ws),
	})
	if err != nil {
		panel.message = fmt.Sprintf("Bind error: %v", err)
		return *m, nil
	}
	panel.message = fmt.Sprintf("Bound %s to this workspace", entry.Adapter)
	return *m, nil
}

func (m *Model) wecomUnbindEntry(adapterName string, panel *wecomPanelState) (Model, tea.Cmd) {
	if m.imManager == nil {
		return *m, nil
	}
	if err := m.imManager.UnbindAdapter(adapterName); err != nil {
		panel.message = fmt.Sprintf("Unbind error: %v", err)
		return *m, nil
	}
	panel.message = "Unbound WeCom from this workspace"
	return *m, nil
}

func (m *Model) wecomBindingEntries() []wecomBindingEntry {
	var entries []wecomBindingEntry
	if m.config == nil {
		return entries
	}

	// Collect runtime adapter states
	adapterStates := make(map[string]im.AdapterState)
	if m.imManager != nil {
		snapshot := m.imManager.Snapshot()
		for _, state := range snapshot.Adapters {
			if state.Name == "" {
				continue
			}
			adapterStates[state.Name] = state
		}
	}

	// Track bindings
	bound := make(map[string]bool)
	occupied := make(map[string]string)
	wsPath := m.currentWorkspacePath()
	if m.imManager != nil {
		if bindings, err := m.imManager.ListBindings(); err == nil {
			for _, b := range bindings {
				if b.Platform == im.PlatformWeCom && b.Workspace != "" {
					bound[b.Adapter] = true
					if b.Workspace != wsPath {
						occupied[b.Adapter] = b.Workspace
					}
				}
			}
		}
	}

	// List wecom adapters from config
	keys := make([]string, 0, len(m.config.IM.Adapters))
	for name, adapter := range m.config.IM.Adapters {
		if adapter.Enabled && strings.EqualFold(adapter.Platform, string(im.PlatformWeCom)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)

	for _, name := range keys {
		var state *im.AdapterState
		if s, ok := adapterStates[name]; ok {
			state = &s
		}
		entries = append(entries, wecomBindingEntry{
			Adapter:      name,
			OccupiedBy:   occupied[name],
			AdapterState: state,
			Bound:        bound[name],
		})
	}
	return entries
}

func defaultWeComTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

// removeWeComEntry fully removes a bot: unbind, stop adapter, delete config.
func (m *Model) removeWeComEntry(entry wecomBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if m.imManager != nil {
			m.imManager.StopAdapter(entry.Adapter)
			_ = m.imManager.UnbindAdapter(entry.Adapter)
		}
		if m.config != nil {
			_ = m.config.RemoveIMAdapter(entry.Adapter)
		}
		return imEditResultMsg{adapterName: entry.Adapter, field: "delete"}
	}
}
