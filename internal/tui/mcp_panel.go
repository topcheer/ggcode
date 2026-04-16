package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/mcp"
)

type mcpPanelState struct {
	selected     int
	message      string
	installMode  bool
	installInput string
}

type mcpInstallResultMsg struct {
	name     string
	replaced bool
	err      error
}

type mcpUninstallResultMsg struct {
	name string
	err  error
}

func (m *Model) openMCPPanel() {
	panel := &mcpPanelState{}
	for i, srv := range m.mcpServers {
		if srv.Connected {
			panel.selected = i
			break
		}
	}
	m.mcpPanel = panel
}

func (m *Model) closeMCPPanel() {
	m.mcpPanel = nil
}

func (m Model) renderMCPPanel() string {
	panel := m.mcpPanel
	if panel == nil {
		return ""
	}
	body := []string{}
	if len(m.mcpServers) == 0 {
		body = append(body, " No MCP servers configured.")
	} else {
		selected := panel.selected
		if selected < 0 || selected >= len(m.mcpServers) {
			selected = 0
		}
		srv := m.mcpServers[selected]
		status := "failed"
		switch {
		case srv.Connected:
			status = "connected"
		case srv.Pending:
			status = "pending"
		}

		body = append(body,
			lipgloss.NewStyle().Bold(true).Render(" Servers"),
			m.renderProviderList(mcpServerLabels(m.mcpServers), selected, true),
			"",
			lipgloss.NewStyle().Bold(true).Render(" Details"),
			fmt.Sprintf(" Name: %s", srv.Name),
			fmt.Sprintf(" Status: %s", status),
			fmt.Sprintf(" Transport: %s", firstNonEmptyValue(srv.Transport, "stdio")),
			fmt.Sprintf(" Tools: %d", len(srv.ToolNames)),
			fmt.Sprintf(" Prompts: %d", len(srv.PromptNames)),
			fmt.Sprintf(" Resources: %d", len(srv.ResourceNames)),
		)
		if srv.Error != "" {
			body = append(body, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(" Error: "+srv.Error))
		}
		body = append(body, "", lipgloss.NewStyle().Bold(true).Render(" Tools"))
		if len(srv.ToolNames) == 0 {
			body = append(body, "  (none discovered yet)")
		} else {
			for _, toolName := range srv.ToolNames {
				body = append(body, "  • "+toolName)
			}
		}
		body = append(body, "", lipgloss.NewStyle().Bold(true).Render(" Prompts"))
		if len(srv.PromptNames) == 0 {
			body = append(body, "  (none)")
		} else {
			for _, promptName := range srv.PromptNames {
				body = append(body, "  • "+promptName)
			}
		}
		body = append(body, "", lipgloss.NewStyle().Bold(true).Render(" Resources"))
		if len(srv.ResourceNames) == 0 {
			body = append(body, "  (none)")
		} else {
			for _, resourceName := range srv.ResourceNames {
				body = append(body, "  • "+resourceName)
			}
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(" Install"))
	if panel.installMode {
		body = append(body,
			" Spec: "+panel.installInput+"█",
			" Format: [name] [-t <stdio|http|ws>] [--env KEY=VALUE ...] [--header KEY:VALUE ...] [-- <command...|url>]",
			" Example: web-reader -t http https://mcp.example.com/api --header Authorization: Bearer xxx",
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Enter install • Esc cancel"),
		)
	} else {
		body = append(body,
			" Press i to install a new MCP server.",
			" Press b to install the built-in Playwright browser automation preset.",
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" j/k move • Enter or r reconnect • i install • b browser preset • x uninstall • Esc close"),
		)
	}
	if panel.message != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Render(panel.message))
	}
	return m.renderContextBox("/mcp", strings.Join(body, "\n"), lipgloss.Color("13"))
}

func (m *Model) handleMCPPanelKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	panel := m.mcpPanel
	if panel == nil {
		return *m, nil
	}
	if panel.installMode {
		switch msg.String() {
		case "esc":
			panel.installMode = false
			panel.installInput = ""
			return *m, nil
		case "enter":
			spec := strings.TrimSpace(panel.installInput)
			if spec == "" {
				panel.message = m.t("panel.mcp.install_spec_required")
				return *m, nil
			}
			panel.installMode = false
			panel.message = m.t("panel.mcp.installing_server")
			return *m, m.installMCPServer(spec)
		case "backspace":
			runes := []rune(panel.installInput)
			if len(runes) > 0 {
				panel.installInput = string(runes[:len(runes)-1])
			}
			return *m, nil
		case "space", " ":
			panel.installInput += " "
			return *m, nil
		}
		if len(msg.Text) > 0 {
			panel.installInput += msg.Text
		}
		return *m, nil
	}
	switch msg.String() {
	case "up", "k":
		if len(m.mcpServers) > 0 {
			panel.selected = (panel.selected - 1 + len(m.mcpServers)) % len(m.mcpServers)
		}
	case "down", "j", "tab":
		if len(m.mcpServers) > 0 {
			panel.selected = (panel.selected + 1) % len(m.mcpServers)
		}
	case "shift+tab":
		if len(m.mcpServers) > 0 {
			panel.selected = (panel.selected - 1 + len(m.mcpServers)) % len(m.mcpServers)
		}
	case "enter", "r", "R":
		if len(m.mcpServers) == 0 {
			break
		}
		if m.mcpManager == nil {
			panel.message = m.t("panel.mcp.reconnect_unavailable")
			break
		}
		name := m.mcpServers[panel.selected].Name
		if m.mcpManager.Retry(name) {
			panel.message = m.t("panel.mcp.reconnecting", name)
		} else {
			panel.message = m.t("panel.mcp.reconnect_failed", name)
		}
	case "i", "I":
		panel.installMode = true
		panel.installInput = ""
		panel.message = ""
	case "b", "B":
		panel.message = m.t("panel.mcp.installing_browser_preset")
		return *m, m.installMCPServer(mcp.BrowserAutomationInstallSpec)
	case "x", "X", "u", "U":
		if len(m.mcpServers) == 0 {
			break
		}
		name := m.mcpServers[panel.selected].Name
		panel.message = m.t("panel.mcp.uninstalling", name)
		return *m, m.uninstallMCPServer(name)
	case "esc":
		m.closeMCPPanel()
	}
	return *m, nil
}

func (m *Model) installMCPServer(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return mcpInstallResultMsg{err: fmt.Errorf("MCP install unavailable without config")}
		}
		server, err := mcp.ParseInstallArgs(strings.Fields(spec))
		if err != nil {
			return mcpInstallResultMsg{err: err}
		}
		replaced := m.config.UpsertMCPServer(server)
		if err := m.config.Save(); err != nil {
			return mcpInstallResultMsg{err: fmt.Errorf("saving config: %w", err)}
		}
		if m.mcpManager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if err := m.mcpManager.Install(ctx, server); err != nil {
				return mcpInstallResultMsg{
					name:     server.Name,
					replaced: replaced,
					err:      fmt.Errorf("saved to config, but connection failed: %w", err),
				}
			}
		}
		return mcpInstallResultMsg{name: server.Name, replaced: replaced}
	}
}

func (m *Model) uninstallMCPServer(name string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return mcpUninstallResultMsg{name: name, err: fmt.Errorf("MCP uninstall unavailable without config")}
		}
		if !m.config.RemoveMCPServer(name) {
			return mcpUninstallResultMsg{name: name, err: fmt.Errorf("MCP server %s is not configured", name)}
		}
		if err := m.config.Save(); err != nil {
			return mcpUninstallResultMsg{name: name, err: fmt.Errorf("saving config: %w", err)}
		}
		if m.mcpManager != nil && !m.mcpManager.Uninstall(name) {
			return mcpUninstallResultMsg{name: name, err: fmt.Errorf("saved config, but runtime uninstall failed for %s", name)}
		}
		return mcpUninstallResultMsg{name: name}
	}
}

func mcpServerLabels(servers []MCPInfo) []string {
	out := make([]string, 0, len(servers))
	for _, srv := range servers {
		status := "✕"
		switch {
		case srv.Connected:
			status = "✓"
		case srv.Pending:
			status = "…"
		}
		out = append(out, fmt.Sprintf("%s %s (%s)", status, srv.Name, firstNonEmptyValue(srv.Transport, "stdio")))
	}
	return out
}
