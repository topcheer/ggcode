package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"strings"
)

// handleMcpServersMsg handles the corresponding message case.
func (m Model) handleMcpServersMsg(msg mcpServersMsg) (Model, tea.Cmd) {
	m.mcpServers = toMCPInfos(msg.Servers)
	m.refreshCommands()
	if m.mcpManager != nil {
		if pending := m.mcpManager.PendingOAuth(); pending != nil {
			m.mcpManager.ClearPendingOAuth()
			return m, m.startMCPOAuth(pending)
		}
	}
	return m, nil

}

// handleMcpInstallResultMsg handles the corresponding message case.
func (m Model) handleMcpInstallResultMsg(msg mcpInstallResultMsg) (Model, tea.Cmd) {
	if m.mcpPanel != nil {
		if msg.err != nil {
			m.mcpPanel.message = fmt.Sprintf("Install failed: %v", msg.err)
		} else if msg.replaced {
			m.mcpPanel.message = fmt.Sprintf("Updated MCP server %s.", msg.name)
		} else {
			m.mcpPanel.message = fmt.Sprintf("Installed MCP server %s.", msg.name)
		}
	}
	return m, nil

}

// handleMcpOAuthStartMsg handles the corresponding message case.
func (m Model) handleMcpOAuthStartMsg(msg mcpOAuthStartMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		if m.mcpPanel != nil {
			m.mcpPanel.message = fmt.Sprintf("MCP OAuth failed for %s: %v", msg.serverName, msg.err)
		}
		return m, nil
	}
	if msg.deviceUserCode != "" {
		// Device flow: store code info for banner display, poll in background
		m.addDeviceCode(msg.serverName, msg.deviceUserCode, msg.authorizeURL)
		if m.mcpPanel != nil {
			m.mcpPanel.message = fmt.Sprintf("Waiting for %s device authorization...", msg.serverName)
		}
		return m, m.waitForMCPOAuthDevice(msg.handler)
	}
	// Browser flow
	// Auto-open MCP panel so user can see the auth instructions
	if m.mcpPanel == nil {
		m.openMCPPanel()
	}
	notes := []string{fmt.Sprintf("Opening browser for MCP server %s authentication...", msg.serverName)}
	if msg.openErr != nil {
		notes = append(notes, fmt.Sprintf("Browser failed: %v", msg.openErr))
		notes = append(notes, fmt.Sprintf("Visit: %s", msg.authorizeURL))
	}
	m.mcpPanel.message = strings.Join(notes, "\n")
	return m, m.waitForMCPOAuthCallback(msg.handler)

}

// handleMcpOAuthResultMsg handles the corresponding message case.
func (m Model) handleMcpOAuthResultMsg(msg mcpOAuthResultMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.removeDeviceCode(msg.serverName)
		if m.mcpPanel != nil {
			m.mcpPanel.message = fmt.Sprintf("MCP OAuth failed for %s: %v", msg.serverName, msg.err)
		}
		return m, nil
	}
	m.removeDeviceCode(msg.serverName)
	if m.mcpPanel != nil {
		m.mcpPanel.message = fmt.Sprintf("MCP server %s authenticated successfully", msg.serverName)
	}
	if m.mcpManager != nil {
		m.mcpManager.Retry(msg.serverName)
	}
	return m, nil

}

// handleMcpUninstallResultMsg handles the corresponding message case.
func (m Model) handleMcpUninstallResultMsg(msg mcpUninstallResultMsg) (Model, tea.Cmd) {
	if m.mcpPanel != nil {
		if msg.err != nil {
			m.mcpPanel.message = fmt.Sprintf("Uninstall failed: %v", msg.err)
		} else {
			m.mcpPanel.message = fmt.Sprintf("Uninstalled MCP server %s.", msg.name)
			if m.mcpPanel.selected >= len(m.mcpServers) && len(m.mcpServers) > 0 {
				m.mcpPanel.selected = len(m.mcpServers) - 1
			}
		}
	}
	return m, nil

}
