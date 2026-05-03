package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

func (m *Model) ExecuteRemoteSlashCommand(text string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return "", false
	}
	switch strings.ToLower(parts[0]) {
	case "/provider":
		return m.executeRemoteProviderCommand(parts), true
	case "/model":
		return m.executeRemoteModelCommand(parts), true
	case "/restart":
		return m.executeRemoteRestartCommand(parts), true
	case "/listim":
		return m.executeRemoteListIM(), true
	case "/muteim":
		return m.executeRemoteMuteIM(parts), true
	case "/muteall":
		return m.executeRemoteMuteAll(), true
	case "/muteself":
		return m.executeRemoteMuteSelf(), true
	case "/help":
		return m.executeRemoteHelp(), true
	case "/config":
		return m.executeRemoteConfig(), true
	default:
		return "", false
	}
}

// ---- Provider / Model ----

func (m *Model) executeRemoteProviderCommand(parts []string) string {
	if m.config == nil {
		return "provider switching is unavailable without config"
	}
	if len(parts) == 1 {
		return m.providerCommandSummary()
	}
	vendor := parts[1]
	endpoints := m.config.EndpointNames(vendor)
	if len(endpoints) == 0 {
		return m.t("command.provider_unknown", vendor, m.vendorNames())
	}
	endpoint := endpoints[0]
	if len(parts) > 2 {
		endpoint = parts[2]
	}
	if err := m.config.SetActiveSelection(vendor, endpoint, ""); err != nil {
		return m.t("command.provider_failed", err)
	}
	if err := m.reloadActiveProvider(); err != nil {
		return m.t("command.provider_failed", err)
	}
	return m.t("command.provider_switched", vendor, m.config.Model)
}

func (m *Model) executeRemoteModelCommand(parts []string) string {
	if m.config == nil {
		return "model switching is unavailable without config"
	}
	if len(parts) == 1 {
		return m.modelCommandSummary()
	}
	model := parts[1]
	if err := m.config.SetActiveSelection(m.config.Vendor, m.config.Endpoint, model); err != nil {
		return m.t("command.model_failed", err)
	}
	if err := m.reloadActiveProvider(); err != nil {
		return m.t("command.model_failed", err)
	}
	return m.t("command.model_switched", model, m.config.Vendor)
}

func (m *Model) providerCommandSummary() string {
	if m.config == nil {
		return ""
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		return m.t("command.provider_failed", err)
	}
	return m.t(
		"command.provider_current",
		resolved.VendorName,
		resolved.EndpointName,
		resolved.Model,
		m.vendorNames(),
		strings.Join(m.config.EndpointNames(m.config.Vendor), ", "),
	)
}

func (m *Model) modelCommandSummary() string {
	if m.config == nil {
		return ""
	}
	resolved, err := m.config.ResolveActiveEndpoint()
	if err != nil {
		return m.t("command.model_failed", err)
	}
	models := uniqueStrings(append([]string(nil), resolved.Models...))
	if len(models) == 0 && strings.TrimSpace(resolved.Model) != "" {
		models = []string{resolved.Model}
	}
	return m.t("command.model_current", resolved.Model, resolved.VendorName, strings.Join(models, ", "))
}

func (m *Model) remoteSwitchChoices() string {
	return fmt.Sprintf("%s%s", m.providerCommandSummary(), m.modelCommandSummary())
}

// ---- Restart ----

func (m *Model) executeRemoteRestartCommand(parts []string) string {
	isDebug := len(parts) > 1 && strings.ToLower(parts[1]) == "debug"
	if isDebug {
		_ = os.Setenv("GGCODE_DEBUG", "1")
		return "RESTART:DEBUG"
	}
	return "RESTART"
}

// scheduleRemoteRestart returns a tea.Cmd that waits briefly (so the IM
// confirmation message is delivered) and then triggers the restart.
func (m *Model) scheduleRemoteRestart() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		m.chatWriteSystem(nextSystemID(), "Restarting ggcode...")
		m.quitting = true
		m.restartRequested = true
		return quitMsg{}
	})
}

// quitMsg is a custom message that triggers a clean quit.
// The Update handler recognizes this and returns tea.Quit.
type quitMsg struct{}

// ---- IM management (aligned with daemon_bridge.go) ----

func (m *Model) executeRemoteListIM() string {
	if m.imManager == nil {
		return "IM manager not available"
	}
	snapshot := m.imManager.Snapshot()
	if len(snapshot.Adapters) == 0 {
		return "📭 No IM adapters configured."
	}

	var sb strings.Builder
	sb.WriteString("📬 IM Adapters:\n")
	for _, a := range snapshot.Adapters {
		status := "✅ online"
		if !a.Healthy {
			status = "❌ " + a.Status
		}
		bound := ""
		for _, binding := range snapshot.CurrentBindings {
			if binding.Adapter == a.Name {
				if binding.Muted {
					bound = " 🔇 muted"
				} else {
					bound = " 📡 active"
				}
				break
			}
		}
		sb.WriteString(fmt.Sprintf("  • %s [%s]%s %s\n", a.Name, a.Platform, bound, status))
	}
	return sb.String()
}

func (m *Model) executeRemoteMuteIM(parts []string) string {
	if m.imManager == nil {
		return "IM manager not available"
	}
	if len(parts) < 2 {
		return "Usage: /muteim <adapter_name>\nUse /listim to see adapter names."
	}
	name := parts[1]
	selfAdapter := m.remoteInboundAdapter
	if name == selfAdapter {
		return "⚠️ Cannot mute yourself. Use /muteself instead."
	}
	if err := m.imManager.MuteBinding(name); err != nil {
		return fmt.Sprintf("Failed to mute %s: %v", name, err)
	}
	return fmt.Sprintf("🔇 Muted adapter: %s", name)
}

func (m *Model) executeRemoteMuteAll() string {
	if m.imManager == nil {
		return "IM manager not available"
	}
	selfAdapter := m.remoteInboundAdapter
	count, err := m.imManager.MuteAllExcept(selfAdapter)
	if err != nil {
		return fmt.Sprintf("Failed: %v", err)
	}
	if selfAdapter != "" {
		return fmt.Sprintf("🔇 Muted %d adapter(s), keeping %s active", count, selfAdapter)
	}
	return fmt.Sprintf("🔇 Muted %d adapter(s)", count)
}

func (m *Model) executeRemoteMuteSelf() string {
	if m.imManager == nil {
		return "IM manager not available"
	}
	adapter := m.remoteInboundAdapter
	if adapter == "" {
		return "Cannot determine your adapter."
	}
	// Return special marker — caller sends warning first, then mutes after delay.
	return "MUTES:" + adapter
}

// scheduleMuteSelf returns a tea.Cmd that waits briefly (so the warning
// message is delivered) and then mutes the adapter.
func (m *Model) scheduleMuteSelf(adapter string) tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		if m.imManager != nil {
			_ = m.imManager.MuteBinding(adapter)
		}
		return nil
	})
}

func (m *Model) executeRemoteHelp() string {
	return "Available commands:\n" +
		"/listim - List IM adapters and their status\n" +
		"/muteim <name> - Mute a specific adapter\n" +
		"/muteall - Mute all adapters except the one you're using\n" +
		"/muteself - Mute THIS adapter (⚠️ you'll stop receiving replies; use /restart from another adapter to recover)\n" +
		"/restart [debug] - Restart ggcode (add 'debug' to enable GGCODE_DEBUG=1)\n" +
		"/provider [vendor] [endpoint] - Show or switch LLM provider\n" +
		"/model [name] - Show or switch model\n" +
		"/config - Show current provider, model and endpoint configuration\n" +
		"/help - Show this help"
}

func (m *Model) executeRemoteConfig() string {
	if m.config == nil {
		return "Config not available"
	}
	return m.remoteSwitchChoices()
}
