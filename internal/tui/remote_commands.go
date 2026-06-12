package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
)

func (m *Model) ExecuteRemoteSlashCommand(text string) (string, bool) {
	if result := im.ExecuteExtendedIMSlashCommand(im.ExtendedIMSlashOptions{
		Manager:     m.imManager,
		SelfAdapter: m.remoteInboundAdapter,
		Text:        text,
		HelpExtraLines: []string{
			"/restart [debug] - Restart ggcode (add 'debug' to enable GGCODE_DEBUG=1)",
			"/provider [vendor] [endpoint] - Show or switch LLM provider",
			"/model [name] - Show or switch model",
			"/stream start|stop|status|config - Control live streaming",
			"/config - Show current provider, model and endpoint configuration",
		},
		OnRestart: func(debug bool) (string, error) {
			if debug {
				return m.executeRemoteRestartCommand([]string{"/restart", "debug"}), nil
			}
			return m.executeRemoteRestartCommand([]string{"/restart"}), nil
		},
		OnProvider: func(vendor, endpoint string) (string, error) {
			parts := []string{"/provider"}
			if vendor != "" {
				parts = append(parts, vendor)
			}
			if endpoint != "" {
				parts = append(parts, endpoint)
			}
			return m.executeRemoteProviderCommand(parts), nil
		},
		OnModel: func(model string) (string, error) {
			parts := []string{"/model"}
			if model != "" {
				parts = append(parts, model)
			}
			return m.executeRemoteModelCommand(parts), nil
		},
		OnConfig: func() (string, error) {
			return m.executeRemoteConfig(), nil
		},
		OnExtra: func(parts []string) (string, bool) {
			switch strings.ToLower(parts[0]) {
			case "/stream":
				resp, _ := m.handleStreamSlash(strings.Join(parts[1:], " "))
				return resp, true
			default:
				return "", false
			}
		},
	}); result.Handled {
		if result.MuteSelfAdapter != "" {
			return "MUTES:" + result.MuteSelfAdapter, true
		}
		return result.Response, true
	}
	return "", false
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
		return remoteRestartMsg{}
	})
}

// remoteRestartMsg triggers a clean restart (quit + execve).
type remoteRestartMsg struct{}

// ---- IM management (aligned with daemon_bridge.go) ----

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

func (m *Model) executeRemoteConfig() string {
	if m.config == nil {
		return "Config not available"
	}
	return m.remoteSwitchChoices()
}
