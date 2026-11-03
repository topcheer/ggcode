package tui

import (
	"context"
	"fmt"
	"github.com/topcheer/ggcode/internal/util"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/plugin"
)

type mcpPanelState struct {
	selected         int
	message          string
	installMode      bool
	installInput     string
	pendingReconnect string
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

type mcpRefreshResultMsg struct {
	name    string
	found   bool
	outcome plugin.RefreshOutcome
	count   int
}

func (m *Model) openMCPPanel() {

	if m.mcpManager != nil {
		if servers := toMCPInfos(m.mcpManager.Snapshot()); len(servers) > 0 {
			m.mcpServers = servers
		}
	}
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

func (m *Model) addDeviceCode(serverName, userCode, verifyURL string) {
	for _, dc := range m.pendingDeviceCodes {
		if dc.serverName == serverName {
			return
		}
	}
	m.pendingDeviceCodes = append(m.pendingDeviceCodes, deviceCodeInfo{
		serverName: serverName,
		userCode:   userCode,
		verifyURL:  verifyURL,
	})
}

func (m *Model) removeDeviceCode(serverName string) {
	for i, dc := range m.pendingDeviceCodes {
		if dc.serverName == serverName {
			m.pendingDeviceCodes = append(m.pendingDeviceCodes[:i], m.pendingDeviceCodes[i+1:]...)
			return
		}
	}
}

func (m Model) renderDeviceCodeBanner() string {
	if len(m.pendingDeviceCodes) == 0 {
		return ""
	}
	var lines []string
	for _, dc := range m.pendingDeviceCodes {
		if dc.userCode == "" {

			lines = append(lines,
				fmt.Sprintf(" MCP server %s requires authentication", dc.serverName),
				fmt.Sprintf(" Visit %s", dc.verifyURL),
			)
			continue
		}
		codeDigits := strings.Join(strings.Split(dc.userCode, ""), "   ")
		codeStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("57")).
			Padding(0, 3).
			Render(codeDigits)
		lines = append(lines,
			fmt.Sprintf(" MCP server %s requires authentication", dc.serverName),
			fmt.Sprintf(" Visit %s and enter:", dc.verifyURL),
			codeStyle,
		)
	}

	return m.renderContextBoxAuto("MCP Device Authorization", strings.Join(lines, "\n"), lipgloss.Color("11"))
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
			func() string {
				if srv.Disabled {
					return " Disabled: yes"
				}
				return ""
			}(),
			fmt.Sprintf(" Transport: %s", util.FirstNonEmpty(srv.Transport, "stdio")),
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
			body = append(body, renderTruncatedList(srv.ToolNames, 4)...)
		}
		body = append(body, "", lipgloss.NewStyle().Bold(true).Render(" Prompts"))
		if len(srv.PromptNames) == 0 {
			body = append(body, "  (none)")
		} else {
			body = append(body, renderTruncatedList(srv.PromptNames, 4)...)
		}
		body = append(body, "", lipgloss.NewStyle().Bold(true).Render(" Resources"))
		if len(srv.ResourceNames) == 0 {
			body = append(body, "  (none)")
		} else {
			body = append(body, renderTruncatedList(srv.ResourceNames, 4)...)
		}
	}

	body = append(body, "", lipgloss.NewStyle().Bold(true).Render(" Install"))
	if panel.installMode {
		body = append(body,
			" Spec: "+panel.installInput+"█",
			" Format: [name] [-t <stdio|http|ws>] [--env KEY=VALUE ...] [--header KEY:VALUE ...] [-- <command...|url>]",
			" Example: web-reader -t http https://mcp.example.com/api --header Authorization: Bearer xxx",
			renderPasteShortcutHint(m.currentLanguage()),
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" Enter install • Esc cancel"),
		)
	} else {
		body = append(body,
			" Press i to install a new MCP server.",
			lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" j/k move • Space toggle • Enter/r reconnect • f refresh tools • a reset auth • i install • x uninstall • Esc close"),
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
		return m.handleMCPInstallInput(panel, msg)
	}
	switch msg.String() {
	case "up", "k", "shift+tab":
		mcpPanelNavigate(panel, -1, len(m.mcpServers))
	case "down", "j", "tab":
		mcpPanelNavigate(panel, +1, len(m.mcpServers))
	case "enter", "r", "R":
		m.mcpPanelReconnect(panel)
	case "f", "F":
		if cmd := m.mcpPanelRefresh(panel); cmd != nil {
			return *m, cmd
		}
	case " ", "space":
		if m.mcpPanelToggle(panel) {
			return *m, nil
		}
	case "i", "I":
		panel.installMode = true
		panel.installInput = ""
		panel.message = ""
	case "a", "A":
		m.mcpPanelResetAuth(panel)
	case "x", "X", "u", "U":
		if len(m.mcpServers) == 0 {
			break
		}
		name := m.mcpServers[panel.selected].Name
		panel.message = m.t("panel.mcp.uninstalling", name)
		return *m, m.uninstallMCPServer(name)
	case "esc", "ctrl+c":
		m.closeMCPPanel()
	}
	return *m, nil
}

func (m *Model) handleMCPInstallInput(panel *mcpPanelState, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
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

func mcpPanelNavigate(panel *mcpPanelState, delta, n int) {
	if n == 0 {
		return
	}
	panel.selected = (panel.selected + delta + n) % n
}

func (m *Model) mcpPanelReconnect(panel *mcpPanelState) {
	if len(m.mcpServers) == 0 {
		return
	}
	if m.mcpManager == nil {
		panel.message = m.t("panel.mcp.reconnect_unavailable")
		return
	}
	name := m.mcpServers[panel.selected].Name
	if m.mcpManager.Retry(name) {
		panel.message = m.t("panel.mcp.reconnecting", name)
		panel.pendingReconnect = name
	} else {
		panel.message = m.t("panel.mcp.reconnect_failed", name)
	}
}

func (m *Model) mcpPanelRefresh(panel *mcpPanelState) tea.Cmd {
	if len(m.mcpServers) == 0 {
		return nil
	}
	if m.mcpManager == nil {
		panel.message = m.t("panel.mcp.refresh_unavailable")
		return nil
	}
	name := m.mcpServers[panel.selected].Name
	panel.message = fmt.Sprintf("Refreshing tools for %s...", name)
	mgr := m.mcpManager
	return func() tea.Msg {
		found, outcome, count := mgr.Refresh(name)
		return mcpRefreshResultMsg{name: name, found: found, outcome: outcome, count: count}
	}
}

func (m *Model) mcpPanelToggle(panel *mcpPanelState) bool {
	if len(m.mcpServers) == 0 {
		return false
	}
	srv := m.mcpServers[panel.selected]
	willDisable := !srv.Disabled
	if err := plugin.SetMCPDisabledIn(workingDirFromModel(m), srv.Name, willDisable); err != nil {

		panel.message = fmt.Sprintf("persist failed (will revert on restart): %v", err)
		return true
	}
	m.mcpServers[panel.selected].Disabled = willDisable
	if willDisable {
		if m.mcpManager != nil {
			m.mcpManager.Disconnect(srv.Name)
		}
		panel.message = fmt.Sprintf(" %s disabled and disconnected", srv.Name)
	} else {
		if m.mcpManager != nil {
			m.mcpManager.Reconnect(srv.Name)
			panel.pendingReconnect = srv.Name
		}
		panel.message = fmt.Sprintf(" %s enabled, reconnecting...", srv.Name)
	}
	return false
}

func (m *Model) mcpPanelResetAuth(panel *mcpPanelState) {
	if len(m.mcpServers) == 0 {
		return
	}
	if m.mcpManager == nil {
		panel.message = m.t("panel.mcp.reconnect_unavailable")
		return
	}
	name := m.mcpServers[panel.selected].Name
	if m.mcpManager.ForceReauth(name) {
		panel.message = fmt.Sprintf(" Reset credentials for %s — reconnecting...", name)
		panel.pendingReconnect = name
	} else {
		panel.message = fmt.Sprintf(" %s: no OAuth handler (not an HTTP/WS server?)", name)
	}
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

		replaced := false
		return configMutationMsg{
			apply: func(m *Model) error {

				replaced = m.config.UpsertMCPServer(server)
				return m.saveConfig()
			},
			next: func(m *Model) tea.Cmd {
				return func() tea.Msg {
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
			},
			fail: func(err error) tea.Msg {
				return mcpInstallResultMsg{err: err}
			},
		}
	}
}

func (m *Model) uninstallMCPServer(name string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return mcpUninstallResultMsg{name: name, err: fmt.Errorf("MCP uninstall unavailable without config")}
		}

		return configMutationMsg{
			apply: func(m *Model) error {
				if !m.config.RemoveMCPServer(name) {

					_ = auth.DefaultStore().Delete("mcp:" + name)
					return fmt.Errorf("MCP server %s is not configured", name)
				}
				if err := m.config.SaveMCPServersScoped(m.configSaveScope); err != nil {
					return fmt.Errorf("saving config: %w", err)
				}
				return nil
			},
			next: func(m *Model) tea.Cmd {
				return func() tea.Msg {
					if m.mcpManager != nil && !m.mcpManager.Uninstall(name) {
						return mcpUninstallResultMsg{name: name, err: fmt.Errorf("saved config, but runtime uninstall failed for %s", name)}
					}

					if derr := auth.DefaultStore().Delete("mcp:" + name); derr != nil {
						return mcpUninstallResultMsg{name: name, err: fmt.Errorf("uninstalled, but deleting stored credentials failed: %w", derr)}
					}
					return mcpUninstallResultMsg{name: name}
				}
			},
			fail: func(err error) tea.Msg {
				return mcpUninstallResultMsg{name: name, err: err}
			},
		}
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
		if srv.Disabled {
			status = "✗"
		}
		out = append(out, fmt.Sprintf("%s %s (%s)", status, srv.Name, util.FirstNonEmpty(srv.Transport, "stdio")))
	}
	return out
}

func renderTruncatedList(items []string, max int) []string {
	out := make([]string, 0, min(len(items), max+1))
	end := len(items)
	if end > max {
		end = max
	}
	for i := 0; i < end; i++ {
		out = append(out, "  • "+items[i])
	}
	if len(items) > max {
		out = append(out, fmt.Sprintf("  ... + %d more", len(items)-max))
	}
	return out
}

func (m *Model) startMCPOAuth(oauthErr *plugin.MCPOAuthRequiredError) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		handler := oauthErr.Handler
		if handler.SupportsDCR() {
			if err := handler.RegisterClient(ctx); err != nil {
				debug.Log("mcp-oauth", "dcr_failed server=%s error=%v, continuing with existing client_id", oauthErr.ServerName, err)

			}
		}

		if handler.SupportsDeviceFlow() {

			scopes := handler.GetScopes()
			devResp, err := handler.StartDeviceFlow(ctx, scopes)
			if err == nil {

				var openErr error
				if m.urlOpener != nil {
					openErr = m.urlOpener(devResp.VerificationURI)
				}
				return mcpOAuthStartMsg{
					serverName:     oauthErr.ServerName,
					authorizeURL:   devResp.VerificationURI,
					handler:        handler,
					openErr:        openErr,
					deviceUserCode: devResp.UserCode,
				}
			}

		}

		authorizeURL, err := handler.StartAuthFlow(ctx)
		if err != nil {
			return mcpOAuthStartMsg{serverName: oauthErr.ServerName, err: err}
		}

		msg := mcpOAuthStartMsg{
			serverName:   oauthErr.ServerName,
			authorizeURL: authorizeURL,
			handler:      handler,
		}
		if m.urlOpener != nil {
			msg.openErr = m.urlOpener(authorizeURL)
		}
		return msg
	}
}

func (m *Model) waitForMCPOAuthCallback(handler *mcp.OAuthHandler) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		code, err := handler.WaitForCallback(ctx)
		if err != nil {
			return mcpOAuthResultMsg{serverName: handler.ServerName(), err: err}
		}

		tokenResp, err := handler.ExchangeCode(ctx, code)
		if err != nil {
			return mcpOAuthResultMsg{serverName: handler.ServerName(), err: err}
		}

		if err := handler.SaveToken(tokenResp); err != nil {
			return mcpOAuthResultMsg{serverName: handler.ServerName(), err: err}
		}
		return mcpOAuthResultMsg{serverName: handler.ServerName()}
	}
}

func (m *Model) waitForMCPOAuthDevice(handler *mcp.OAuthHandler) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		tokenResp, err := handler.PollDeviceToken(ctx)
		if err != nil {
			return mcpOAuthResultMsg{serverName: handler.ServerName(), err: err}
		}

		if err := handler.SaveToken(tokenResp); err != nil {
			return mcpOAuthResultMsg{serverName: handler.ServerName(), err: err}
		}
		return mcpOAuthResultMsg{serverName: handler.ServerName()}
	}
}
