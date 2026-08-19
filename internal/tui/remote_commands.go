package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
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
		HelpExtraLines: append([]string{
			"/restart [debug] - Restart ggcode (add 'debug' to enable GGCODE_DEBUG=1)",
			"/provider [vendor] [endpoint] - Show or switch LLM provider",
			"/model [name] - Show or switch model",
			"/stream start|stop|status|config - Control live streaming",
			"/config - Show current provider, model and endpoint configuration",
		}, im.IMSlashHelpLines()...),
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
				// Shared registry dispatch (path B): the same table the daemon
				// bridge serves from, with the live model as deps.
				return im.ExecuteRegistrySlashCommand(tuiSlashDeps{m}, strings.Join(parts, " "))
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

// tuiSlashDeps implements im.SlashDeps from the live TUI model state -
// the path-B counterpart of the daemon bridge's implementation. Where the
// bridge reads disk stores, the TUI prefers its in-memory session (richer
// and current-turn fresh), falling back to the shared disk renderers.
type tuiSlashDeps struct{ m *Model }

func (d tuiSlashDeps) SessionCostSummary() (string, error) {
	if d.m.session != nil {
		usage := d.m.session.TokenUsage
		if usage.Total() > 0 {
			var sb strings.Builder
			sb.WriteString("Session Cost:\n\n")
			sb.WriteString(fmt.Sprintf("  Model:  %s (%s)\n", d.m.session.Model, d.m.session.Vendor))
			sb.WriteString(fmt.Sprintf("  Input tokens:  %s\n", humanizeTokenCount(usage.InputTokens)))
			sb.WriteString(fmt.Sprintf("  Output tokens: %s\n", humanizeTokenCount(usage.OutputTokens)))
			if usage.CacheRead > 0 {
				sb.WriteString(fmt.Sprintf("  Cache read:    %s\n", humanizeTokenCount(usage.CacheRead)))
			}
			if usage.CacheWrite > 0 {
				sb.WriteString(fmt.Sprintf("  Cache write:   %s\n", humanizeTokenCount(usage.CacheWrite)))
			}
			return sb.String(), nil
		}
	}
	return im.BuildCrossSessionCostSummary()
}

func (d tuiSlashDeps) SessionUsageSummary() (string, error) {
	if d.m.session != nil && d.m.session.TokenUsage.Total() > 0 {
		u := d.m.session.TokenUsage
		var sb strings.Builder
		sb.WriteString("Session Token Usage:\n\n")
		sb.WriteString(fmt.Sprintf("  Input tokens:  %s\n", humanizeTokenCount(u.InputTokens)))
		sb.WriteString(fmt.Sprintf("  Output tokens: %s\n", humanizeTokenCount(u.OutputTokens)))
		if u.CacheRead > 0 {
			sb.WriteString(fmt.Sprintf("  Cache read:    %s\n", humanizeTokenCount(u.CacheRead)))
		}
		if u.CacheWrite > 0 {
			sb.WriteString(fmt.Sprintf("  Cache write:   %s\n", humanizeTokenCount(u.CacheWrite)))
		}
		return sb.String(), nil
	}
	return im.BuildCrossSessionCostSummary()
}

func (d tuiSlashDeps) CurrentMode() string {
	return d.m.mode.String()
}

func (d tuiSlashDeps) SwitchMode(name string) error {
	// Mirror handleModeCommand semantics: parse, apply to model + policy,
	// drop stale approvals, persist the preference to session metadata.
	newMode := permission.ParsePermissionMode(name)
	d.m.mode = newMode
	if cp, ok := d.m.policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(newMode)
	}
	d.m.clearPendingApprovals()
	d.m.persistModePreference()
	return nil
}

func (d tuiSlashDeps) ToolList() (string, error) {
	if d.m.agent == nil {
		return "Agent not initialized.", nil
	}
	reg := d.m.agent.ToolRegistry()
	if reg == nil {
		return "Tool registry not available.", nil
	}
	tools := reg.List()
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name() < tools[j].Name() })
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Available tools (%d):\n\n", len(tools)))
	for _, t := range tools {
		desc := t.Description()
		if len(desc) > 120 {
			desc = desc[:117] + "..."
		}
		sb.WriteString(fmt.Sprintf("  %-20s %s\n", t.Name(), desc))
	}
	return sb.String(), nil
}

func (d tuiSlashDeps) ModifiedFiles() (string, error) {
	if d.m.agent == nil {
		return "Agent not initialized.", nil
	}
	cpMgr := d.m.agent.CheckpointManager()
	if cpMgr == nil {
		return "Checkpoints not available.", nil
	}
	files := cpMgr.ModifiedFiles()
	if len(files) == 0 {
		return "No files modified yet this session.", nil
	}
	totalEdits := 0
	for _, f := range files {
		totalEdits += f.Edits
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Modified files (%d, %d edits):\n\n", len(files), totalEdits))
	for _, f := range files {
		flag := ""
		if f.IsNew {
			flag = " (new)"
		}
		sb.WriteString(fmt.Sprintf("  %s - %d edits%s\n", f.Path, f.Edits, flag))
	}
	return sb.String(), nil
}

func (d tuiSlashDeps) GitDiff(args []string) (string, error) {
	gitArgs := append([]string{"diff"}, args...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = workingDirFromModel(d.m)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("git diff: %v", err)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		// Clean tree: git diff exits 0 with empty output. One-shot command
		// contracts (parity test + IM bridge) require a non-empty response.
		return "No changes.", nil
	}
	return trimmed, nil
}
