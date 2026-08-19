package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
)

const (
	// restartFallbackTimeout bounds how long an armed agent-requested restart
	// waits for the current turn to finish before force-restarting (#347).
	restartFallbackTimeout = 30 * time.Second
)

// armRestartMsg arms a session-preserving restart requested by the LLM
// restart tool (#347). Unlike remoteRestartMsg (IM /restart), it defers the
// quit until the agent turn completes so sibling tool results and trailing
// assistant text are persisted.
type armRestartMsg struct {
	debug bool
}

// restartFallbackMsg fires after restartFallbackTimeout when an armed
// restart is still waiting for the turn to finish.
type restartFallbackMsg struct{}

// noteTurnActivity records that the agent turn is making progress (stream
// chunks, reasoning, tool results). The armed-restart fallback uses this to
// distinguish a stalled turn (force restart) from a legitimately long-running
// tool batch (keep waiting, re-arm) (#362).
func (m *Model) noteTurnActivity() {
	m.lastTurnActivityAt = time.Now()
}

// firePendingRestartCmd executes the armed restart via the same proven
// remoteRestartMsg path (quit flags → shutdownAll → tea.Quit → execRestart).
func firePendingRestartCmd() tea.Cmd {
	return func() tea.Msg {
		return remoteRestartMsg{}
	}
}

// armRestartFallbackCmd schedules the 30s force-restart fallback.
func armRestartFallbackCmd() tea.Cmd {
	return tea.Tick(restartFallbackTimeout, func(time.Time) tea.Msg {
		return restartFallbackMsg{}
	})
}

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
			case "/cost", "/mode":
				// Query-style agent commands: shared with the daemon bridge
				// path (im.ExecuteAgentSlashCommand). In the TUI-attached
				// mode these read live model state where available.
				return m.executeAgentSlashQuery(strings.ToLower(parts[0]))
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
		// Explicit: the user directly requested /restart (IM/desktop/webui).
		// Explicit requests are exempt from the armed-restart loading guard —
		// instead of being silently swallowed mid-turn they queue until the
		// turn ends (#374).
		return remoteRestartMsg{explicit: true}
	})
}

// remoteRestartMsg triggers a clean restart (quit + execve).
type remoteRestartMsg struct {
	// explicit marks a direct user request (IM /restart, desktop/webui
	// InjectRestart) as opposed to the agent-armed restart tool's deferred
	// fire (#374).
	explicit bool
}

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

// executeAgentSlashQuery serves /cost and /mode for IM-attached TUI sessions.
// Unlike the daemon path (which reads disk), the TUI has live state: the
// in-memory session for /cost and the model's policy for /mode.
func (m *Model) executeAgentSlashQuery(cmd string) (string, bool) {
	switch cmd {
	case "/cost":
		// Prefer live session usage (same data the local /cost shows); fall
		// back to the cross-session disk summary when no usage is recorded.
		if m.session != nil {
			usage := m.session.TokenUsage
			if usage.Total() > 0 {
				var sb strings.Builder
				sb.WriteString("Session Cost:\n\n")
				sb.WriteString(fmt.Sprintf("  Model:  %s (%s)\n", m.session.Model, m.session.Vendor))
				sb.WriteString(fmt.Sprintf("  Input tokens:  %s\n", humanizeTokenCount(usage.InputTokens)))
				sb.WriteString(fmt.Sprintf("  Output tokens: %s\n", humanizeTokenCount(usage.OutputTokens)))
				if usage.CacheRead > 0 {
					sb.WriteString(fmt.Sprintf("  Cache read:    %s\n", humanizeTokenCount(usage.CacheRead)))
				}
				if usage.CacheWrite > 0 {
					sb.WriteString(fmt.Sprintf("  Cache write:   %s\n", humanizeTokenCount(usage.CacheWrite)))
				}
				return sb.String(), true
			}
		}
		summary, err := im.BuildCrossSessionCostSummary()
		if err != nil {
			return fmt.Sprintf("Cost query failed: %v", err), true
		}
		return summary, true
	case "/mode":
		return "Current permission mode: " + m.mode.String(), true
	}
	return "", false
}
