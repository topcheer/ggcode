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

func (m *Model) addDeviceCode(serverName, userCode, verifyURL string) {
	for _, dc := range m.pendingDeviceCodes {
		if dc.serverName == serverName {
			return // already showing
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
	return m.renderContextBox("MCP Device Authorization", strings.Join(lines, "\n"), lipgloss.Color("11"))
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
	case "f", "F":
		if len(m.mcpServers) == 0 {
			break
		}
		if m.mcpManager == nil {
			panel.message = m.t("panel.mcp.refresh_unavailable")
			break
		}
		name := m.mcpServers[panel.selected].Name
		found, outcome, count := m.mcpManager.Refresh(name)
		switch {
		case !found:
			panel.message = m.t("panel.mcp.reconnect_failed", name)
		case outcome == plugin.RefreshNotConnected:
			panel.message = m.t("panel.mcp.refresh_not_connected", name)
		case outcome == plugin.RefreshThrottled:
			panel.message = m.t("panel.mcp.refresh_throttled")
		case outcome == plugin.RefreshFailed:
			panel.message = m.t("panel.mcp.refresh_failed", name)
		case outcome == plugin.RefreshUnchanged:
			panel.message = m.t("panel.mcp.refresh_unchanged", name, count)
		default:
			panel.message = m.t("panel.mcp.refreshed", name, count)
		}
	case " ", "space":
		if len(m.mcpServers) == 0 {
			break
		}
		srv := m.mcpServers[panel.selected]
		willDisable := !srv.Disabled
		plugin.SetMCPDisabled(srv.Name, willDisable)
		m.mcpServers[panel.selected].Disabled = willDisable
		if willDisable {
			if m.mcpManager != nil {
				m.mcpManager.Disconnect(srv.Name)
			}
			panel.message = fmt.Sprintf(" %s disabled and disconnected", srv.Name)
		} else {
			if m.mcpManager != nil {
				m.mcpManager.Reconnect(srv.Name)
			}
			panel.message = fmt.Sprintf(" %s enabled, reconnecting...", srv.Name)
		}
	case "i", "I":
		panel.installMode = true
		panel.installInput = ""
		panel.message = ""
	case "a", "A":
		if len(m.mcpServers) == 0 {
			break
		}
		if m.mcpManager == nil {
			panel.message = m.t("panel.mcp.reconnect_unavailable")
			break
		}
		name := m.mcpServers[panel.selected].Name
		if m.mcpManager.ForceReauth(name) {
			panel.message = fmt.Sprintf(" Reset credentials for %s — reconnecting...", name)
		} else {
			panel.message = fmt.Sprintf(" %s: no OAuth handler (not an HTTP/WS server?)", name)
		}
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

func (m *Model) installMCPServer(spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return mcpInstallResultMsg{err: fmt.Errorf("MCP install unavailable without config")}
		}
		server, err := mcp.ParseInstallArgs(strings.Fields(spec))
		if err != nil {
			return mcpInstallResultMsg{err: err}
		}
		// #1382-B (family site 16, MCPServers slice): UpsertMCPServer + save
		// ran HERE on the Cmd goroutine while the UI reads the slice -
		// same family, slice flavor (torn header reads). The write +
		// persist ride configMutationMsg onto the Update loop; the network
		// connect (slow IO) stays on the follow-up Cmd.
		// #1382-B: apply runs before next() (handleConfigMutationMsg is
		// sequential), so a closure-local var threads the replaced flag
		// without any package-level shared state.
		replaced := false
		return configMutationMsg{
			apply: func(m *Model) error {
				// slice write + persist on the Update loop.
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
		// #1382-C: the OAuth token delete ran LAST and was skipped by every
		// early return (and a retry hit 'not configured' first) - one failed
		// uninstall left mcp:<name> credentials on disk FOREVER. Token
		// cleanup now runs FIRST via defer-style ordering and its error is
		// surfaced, never dropped.
		// #1382-B: the config write + persist ride configMutationMsg (family
		// site 16, MCPServers slice).
		return configMutationMsg{
			apply: func(m *Model) error {
				if !m.config.RemoveMCPServer(name) {
					// Still attempt token cleanup - the config entry may already
					// be gone from a previous partial uninstall.
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
					// Token cleanup after a successful uninstall; error surfaced.
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

// renderTruncatedList renders up to max items with "•" prefix, and a summary for the rest.
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
				// DCR failed, but we may still have a client_id from config or built-in mapping
			}
		}

		// Try device flow first (no client_secret needed, no callback server needed)
		if handler.SupportsDeviceFlow() {
			// Full server-declared scopes (#735): least-privilege is guaranteed by
			// the server's scopes_supported, client-side truncation only broke it.
			scopes := handler.GetScopes()
			devResp, err := handler.StartDeviceFlow(ctx, scopes)
			if err == nil {
				// Open verification URI in browser
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
			// Device flow failed, fall through to browser flow
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
