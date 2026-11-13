package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/usage"
	"github.com/topcheer/ggcode/internal/util"
	"github.com/topcheer/ggcode/internal/webui"
)

const (
	restartFallbackTimeout = 30 * time.Second
	// #5367: hard cap on how long an armed restart may keep deferring behind
	// continuously active turns. Without it, autonomous/cron loops starve the
	// restart forever while the arm announcement already claimed success.
	restartArmHardCap = 5 * time.Minute
)

type armRestartMsg struct {
	debug bool

	reason string
}

type restartFallbackMsg struct{}

func (m *Model) noteTurnActivity() {
	m.lastTurnActivityAt = time.Now()
}

func firePendingRestartCmd() tea.Cmd {
	return func() tea.Msg {
		return remoteRestartMsg{}
	}
}

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

func (m *Model) executeRemoteRestartCommand(parts []string) string {
	isDebug := len(parts) > 1 && strings.ToLower(parts[1]) == "debug"
	if isDebug {
		_ = os.Setenv("GGCODE_DEBUG", "1")
		return "RESTART:DEBUG"
	}
	return "RESTART"
}

func (m *Model) scheduleRemoteRestart() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {

		return remoteRestartMsg{explicit: true}
	})
}

type remoteRestartMsg struct {
	explicit bool
}

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

type tuiSlashDeps struct{ m *Model }

func (d tuiSlashDeps) SetLanIdentity(nick, role, team string) (string, error) {
	if d.m.lanChatHub == nil {
		return "", fmt.Errorf("LAN chat is not available (A2A not enabled)")
	}
	if nick == "" {
		return fmt.Sprintf("Current: %s (role: %s, team: %s, agent: %s)",
			d.m.lanChatHub.HumanNick(), d.m.lanChatHub.Role(), d.m.lanChatHub.Team(), d.m.lanChatHub.AgentNick()), nil
	}
	if team == "" {
		team = d.m.lanChatHub.WorkspaceDefaultTeam()
	}
	if err := d.m.lanChatHub.SetNickRoleTeam(nick, role, team); err != nil {
		return "", fmt.Errorf("failed to set nickname: %w", err)
	}
	return fmt.Sprintf("Set: %s (role: %s, team: %s, agent: %s)",
		d.m.lanChatHub.HumanNick(), role, team, d.m.lanChatHub.AgentNick()), nil
}

func (d tuiSlashDeps) EtunnelControl(args []string) (string, error) {
	m := d.m
	if m == nil || m.config == nil {
		return "et tunnel is not available in this session", nil
	}
	var ctl webui.EtunnelController
	if m.webuiTunnel != nil {
		ctl = m.webuiTunnel
	}
	save := func(subdomain, token string) error {
		m.config.ETunnel.Subdomain = subdomain
		m.config.ETunnel.Token = token
		return m.config.SaveScoped(m.configSaveScope)
	}
	return webui.ControlEt(ctl, m.webuiURL, m.config.ETunnel.Subdomain, m.config.ETunnel.Token, m.config.ETunnel.Plan, save, args)
}

func (d tuiSlashDeps) SessionCostSummary() (string, error) {
	if d.m.session != nil {

		// #2319: the local /cost twin takes sessionMutex for this exact
		// read (#1366-C - "a data race that trips -race when /cost is
		// pressed during streaming"); the IM path was the un-synced twin,
		// racing model.go's guarded TokenUsage.Add writes.
		// #2319: same lock as the local twin (see SessionCostSummary above).
		mu := d.m.sessionMutex()
		mu.Lock()
		usage := d.m.session.TokenUsage
		model, vendor := d.m.session.Model, d.m.session.Vendor
		mu.Unlock()
		if usage.Total() > 0 {
			var sb strings.Builder
			sb.WriteString("Session Cost:\n\n")
			sb.WriteString(fmt.Sprintf("  Model:  %s (%s)\n", model, vendor))
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
	// #2319: same lock as the local twin (see SessionCostSummary above).
	var sb strings.Builder
	if d.m.session != nil {

		mu := d.m.sessionMutex()
		mu.Lock()
		u := d.m.session.TokenUsage
		mu.Unlock()
		if u.Total() > 0 {
			sb.WriteString("Session Token Usage:\n\n")
			sb.WriteString(fmt.Sprintf("  Input tokens:  %s\n", humanizeTokenCount(u.InputTokens)))
			sb.WriteString(fmt.Sprintf("  Output tokens: %s\n", humanizeTokenCount(u.OutputTokens)))
			if u.CacheRead > 0 {
				sb.WriteString(fmt.Sprintf("  Cache read:    %s\n", humanizeTokenCount(u.CacheRead)))
			}
			if u.CacheWrite > 0 {
				sb.WriteString(fmt.Sprintf("  Cache write:   %s\n", humanizeTokenCount(u.CacheWrite)))
			}
		}
	}

	if vendor := util.FirstNonEmpty(d.m.activeVendor, d.m.startupVendor); vendor != "" {
		if svc := d.m.ensureUsageService(); svc != nil && svc.Has(vendor) {
			baseURL, apiKey := d.m.resolveVendorEndpoint(vendor)
			if apiKey != "" {

				// #2377: snapshot the EMITTER VALUE on the Update side. The
				// first async cut captured the whole *Model (`emitter := d.m`)
				// so emitIMText re-read m.imEmitter from the probe goroutine
				// while storeIMRuntime/syncIMRuntimeCache/storeIMInstanceDetect
				// write it on Update - a data race the -race detector owns.
				// Reading the field here (same goroutine as the writers) and
				// calling EmitText directly on the snapshot closes it.
				emitter := d.m.imEmitter
				if emitter != nil {

					safego.Go("tui.usageProbe.emit", func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						info, err := svc.Get(ctx, vendor, baseURL, apiKey)
						cancel()
						if err == nil && info != nil {
							emitter.EmitText(usage.RenderText(vendor, info, nil))
						}
					})
				}
			}
		}
	}
	if sb.Len() > 0 {
		return sb.String(), nil
	}
	return im.BuildCrossSessionCostSummary()
}

func (d tuiSlashDeps) CurrentMode() string {
	return d.m.mode.String()
}

func (d tuiSlashDeps) SwitchMode(name string) error {

	if !permission.IsValidPermissionMode(name) {
		return fmt.Errorf("unknown permission mode %q (valid: supervised, bypass; legacy aliases auto/autopilot normalize)", name)
	}
	newMode := permission.ParsePermissionMode(name)
	if (newMode == permission.BypassMode || newMode == permission.AutopilotMode) &&
		(d.m.config == nil || !d.m.config.IM.RemoteDangerousCommands) {
		return fmt.Errorf("switching to %s over IM requires the im.remote_dangerous_commands opt-in (config yaml) - refusing zero-confirmation privilege escalation (#2185)", newMode)
	}
	d.m.mode = newMode
	d.m.modeLive.set(newMode) // cross-copy publish (#5454)
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

	gitArgs := append([]string{"diff"}, im.SanitizeGitDiffArgs(args)...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = workingDirFromModel(d.m)
	out, err := cmd.CombinedOutput()
	if err != nil {

		trimmed := strings.TrimRight(string(out), "\n")
		if strings.HasPrefix(trimmed, "diff ") {
			return trimmed, nil
		}
		return "", fmt.Errorf("git diff: %v", err)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {

		return "No changes.", nil
	}
	return trimmed, nil
}
