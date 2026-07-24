package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/util"
)

// imPanelConfig captures per-adapter differences
type imPanelConfig struct {
	adapterType      string // "slack", "discord", "feishu", etc.
	platform         im.Platform
	title            string
	color            string // lipgloss color string for the panel border
	parseCreateSpec  func(spec string) (map[string]string, error)
	createAdapter    func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig
	openPanelCmd     func() tea.Cmd
	extraKeyHandlers map[string]func(*Model, imBindingEntry, tea.KeyPressMsg) (Model, tea.Cmd, bool)
	hasShareFeatures bool
	showMutedInList  bool
}

// genericIMPanelState is the generic panel state for adapter-specific panels
type genericIMPanelState struct {
	selected     int
	message      string
	createMode   bool
	createInput  string
	editState    imAdapterEditState
	shareAdapter string
	shareLink    string
	shareQRCode  string
}

// imBindingEntry is the generic binding entry
type imBindingEntry struct {
	Adapter          string
	Label            string
	TargetID         string
	ChannelID        string
	WorkspaceChannel string
	OccupiedBy       string
	AdapterState     *im.AdapterState
	Disabled         bool
	Muted            bool
}

// imBindResultMsg is the generic bind result message
type imBindResultMsg struct {
	message      string
	err          error
	shareAdapter string
	shareLink    string
	shareQRCode  string
}

// imPanelConfigs is a registry of adapter configs
var imPanelConfigs = map[string]imPanelConfig{
	"slack": {
		adapterType: "slack",
		platform:    im.PlatformSlack,
		title:       "/slack",
		color:       "2",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 3 {
				return nil, errors.New("invalid format: expected name bot_token app_token")
			}
			return map[string]string{
				"name":      strings.TrimSpace(fields[0]),
				"bot_token": strings.TrimSpace(fields[1]),
				"app_token": strings.TrimSpace(fields[2]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"bot_token": fields["bot_token"],
					"app_token": fields["app_token"],
				},
			}
		},
	},
	"discord": {
		adapterType: "discord",
		platform:    im.PlatformDiscord,
		title:       "/discord",
		color:       "55",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 2 {
				return nil, errors.New("invalid format: expected name token")
			}
			return map[string]string{
				"name":  strings.TrimSpace(fields[0]),
				"token": strings.TrimSpace(fields[1]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"token": fields["token"],
				},
			}
		},
	},
	"feishu": {
		adapterType: "feishu",
		platform:    im.PlatformFeishu,
		title:       "/feishu",
		color:       "11",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 3 {
				return nil, errors.New("invalid format: expected name app_id app_secret")
			}
			return map[string]string{
				"name":       strings.TrimSpace(fields[0]),
				"app_id":     strings.TrimSpace(fields[1]),
				"app_secret": strings.TrimSpace(fields[2]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"app_id":     fields["app_id"],
					"app_secret": fields["app_secret"],
				},
			}
		},
	},
	"qq": {
		adapterType:      "qq",
		platform:         im.PlatformQQ,
		title:            "/qq",
		color:            "13",
		hasShareFeatures: true,
		showMutedInList:  true,
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 3 {
				return nil, errors.New("invalid format: expected name appid appsecret")
			}
			return map[string]string{
				"name":      strings.TrimSpace(fields[0]),
				"appid":     strings.TrimSpace(fields[1]),
				"appsecret": strings.TrimSpace(fields[2]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"appid":     fields["appid"],
					"appsecret": fields["appsecret"],
				},
			}
		},
		extraKeyHandlers: map[string]func(*Model, imBindingEntry, tea.KeyPressMsg) (Model, tea.Cmd, bool){
			"c": func(m *Model, entry imBindingEntry, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
				if m.qqPanel == nil {
					return *m, nil, false
				}
				return *m, m.generateIMPanelShareLink(entry), true
			},
		},
	},
	"whatsapp": {
		adapterType: "whatsapp",
		platform:    im.PlatformWhatsApp,
		title:       "/whatsapp",
		color:       "34",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			name := strings.TrimSpace(spec)
			if name == "" {
				return nil, errors.New("adapter name required")
			}
			return map[string]string{
				"name": name,
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra:    map[string]interface{}{},
			}
		},
	},
	"dingtalk": {
		adapterType: "dingtalk",
		platform:    im.PlatformDingTalk,
		title:       "/dingtalk",
		color:       "208",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 3 {
				return nil, errors.New("invalid format: expected name app_key app_secret")
			}
			return map[string]string{
				"name":       strings.TrimSpace(fields[0]),
				"app_key":    strings.TrimSpace(fields[1]),
				"app_secret": strings.TrimSpace(fields[2]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"app_key":    fields["app_key"],
					"app_secret": fields["app_secret"],
				},
			}
		},
	},
	"telegram": {
		adapterType: "telegram",
		platform:    im.PlatformTelegram,
		title:       "/telegram",
		color:       "39",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 2 {
				return nil, errors.New("invalid format: expected name token")
			}
			return map[string]string{
				"name":  strings.TrimSpace(fields[0]),
				"token": strings.TrimSpace(fields[1]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"token": fields["token"],
				},
			}
		},
	},
	"wechat": {
		adapterType: "wechat",
		platform:    im.PlatformWechat,
		title:       "/wechat",
		color:       "2",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 3 {
				return nil, errors.New("invalid format: expected name app_id app_secret")
			}
			return map[string]string{
				"name":       strings.TrimSpace(fields[0]),
				"app_id":     strings.TrimSpace(fields[1]),
				"app_secret": strings.TrimSpace(fields[2]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"app_id":     fields["app_id"],
					"app_secret": fields["app_secret"],
				},
			}
		},
	},
	"wecom": {
		adapterType: "wecom",
		platform:    im.PlatformWeCom,
		title:       "/wecom",
		color:       "220",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 4 {
				return nil, errors.New("invalid format: expected name corp_id agent_id secret")
			}
			return map[string]string{
				"name":     strings.TrimSpace(fields[0]),
				"corp_id":  strings.TrimSpace(fields[1]),
				"agent_id": strings.TrimSpace(fields[2]),
				"secret":   strings.TrimSpace(fields[3]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"corp_id":  fields["corp_id"],
					"agent_id": fields["agent_id"],
					"secret":   fields["secret"],
				},
			}
		},
	},
	"mattermost": {
		adapterType: "mattermost",
		platform:    im.PlatformMattermost,
		title:       "/mattermost",
		color:       "163",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 3 {
				return nil, errors.New("invalid format: expected name url token")
			}
			return map[string]string{
				"name":  strings.TrimSpace(fields[0]),
				"url":   strings.TrimSpace(fields[1]),
				"token": strings.TrimSpace(fields[2]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"url":   fields["url"],
					"token": fields["token"],
				},
			}
		},
	},
	"matrix": {
		adapterType: "matrix",
		platform:    im.PlatformMatrix,
		title:       "/matrix",
		color:       "35",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 3 {
				return nil, errors.New("invalid format: expected name homeserver user_id access_token")
			}
			return map[string]string{
				"name":         strings.TrimSpace(fields[0]),
				"homeserver":   strings.TrimSpace(fields[1]),
				"user_id":      strings.TrimSpace(fields[2]),
				"access_token": strings.TrimSpace(fields[3]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"homeserver":   fields["homeserver"],
					"user_id":      fields["user_id"],
					"access_token": fields["access_token"],
				},
			}
		},
	},
	"signal": {
		adapterType: "signal",
		platform:    im.PlatformSignal,
		title:       "/signal",
		color:       "33",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 2 {
				return nil, errors.New("invalid format: expected name phone_number")
			}
			return map[string]string{
				"name":         strings.TrimSpace(fields[0]),
				"phone_number": strings.TrimSpace(fields[1]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"phone_number": fields["phone_number"],
				},
			}
		},
	},
	"irc": {
		adapterType: "irc",
		platform:    im.PlatformIRC,
		title:       "/irc",
		color:       "94",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 3 {
				return nil, errors.New("invalid format: expected name server nickname")
			}
			return map[string]string{
				"name":     strings.TrimSpace(fields[0]),
				"server":   strings.TrimSpace(fields[1]),
				"nickname": strings.TrimSpace(fields[2]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"server":   fields["server"],
					"nickname": fields["nickname"],
				},
			}
		},
	},
	"nostr": {
		adapterType: "nostr",
		platform:    im.PlatformNostr,
		title:       "/nostr",
		color:       "196",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 2 {
				return nil, errors.New("invalid format: expected name nsec")
			}
			return map[string]string{
				"name": strings.TrimSpace(fields[0]),
				"nsec": strings.TrimSpace(fields[1]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"nsec": fields["nsec"],
				},
			}
		},
	},
	"twitch": {
		adapterType: "twitch",
		platform:    im.PlatformTwitch,
		title:       "/twitch",
		color:       "165",
		parseCreateSpec: func(spec string) (map[string]string, error) {
			fields := strings.Fields(spec)
			if len(fields) < 2 {
				return nil, errors.New("invalid format: expected name token")
			}
			return map[string]string{
				"name":  strings.TrimSpace(fields[0]),
				"token": strings.TrimSpace(fields[1]),
			}, nil
		},
		createAdapter: func(cfg *imPanelConfig, name string, fields map[string]string) config.IMAdapterConfig {
			return config.IMAdapterConfig{
				Enabled:  true,
				Platform: string(cfg.platform),
				Extra: map[string]interface{}{
					"token": fields["token"],
				},
			}
		},
	},
}

// Generic helper functions

func maxIM(v, min int) int {
	if v < min {
		return min
	}
	return v
}

func defaultIMTargetID(workspace string) string {
	base := filepath.Base(strings.TrimSpace(workspace))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "current-cli"
	}
	return base
}

func imStatePtr(state im.AdapterState) *im.AdapterState {
	if strings.TrimSpace(state.Name) == "" {
		return nil
	}
	copy := state
	return &copy
}

func waitForIMAdapterHealthy(m *Model, mgr *im.Manager, adapter string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastStatus im.AdapterState
	for time.Now().Before(deadline) {
		snapshot := mgr.Snapshot()
		for _, state := range snapshot.Adapters {
			if state.Name != adapter {
				continue
			}
			lastStatus = state
			if state.Healthy {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastStatus.Name != "" {
		if strings.TrimSpace(lastStatus.LastError) != "" {
			return errors.New(m.t("panel.generic.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.LastError)))
		}
		if strings.TrimSpace(lastStatus.Status) != "" {
			return errors.New(m.t("panel.generic.error.not_online_detail", adapter, strings.TrimSpace(lastStatus.Status)))
		}
	}
	return errors.New(m.t("panel.generic.error.not_online", adapter))
}

func currentIMBindings(mgr *im.Manager, platform im.Platform) []im.ChannelBinding {
	if mgr == nil {
		return nil
	}
	var result []im.ChannelBinding
	for _, b := range mgr.CurrentBindings() {
		if b.Platform == platform {
			result = append(result, b)
		}
	}
	return result
}

func imBindingEntries(m *Model, cfg imPanelConfig) []imBindingEntry {
	if m.config == nil {
		return nil
	}
	occupied := make(map[string]string)
	adapterStates := make(map[string]im.AdapterState)
	bindingByAdapter := make(map[string]im.ChannelBinding)
	currentWorkspace := strings.TrimSpace(m.currentWorkspacePath())
	if m.imManager != nil {
		snapshot := m.imManager.Snapshot()
		for _, state := range snapshot.Adapters {
			adapterStates[state.Name] = state
		}
		for _, b := range currentIMBindings(m.imManager, cfg.platform) {
			bindingByAdapter[b.Adapter] = b
		}
		if bindings, err := m.imManager.ListBindings(); err == nil {
			for _, binding := range bindings {
				occupied[binding.Adapter] = binding.Workspace
			}
		}
	}
	keys := make([]string, 0, len(m.config.IM.Adapters))
	for name, adapter := range m.config.IM.Adapters {
		if strings.EqualFold(adapter.Platform, string(cfg.platform)) {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	entries := make([]imBindingEntry, 0)
	for _, name := range keys {
		targetID := defaultIMTargetID(currentWorkspace)
		workspaceChannel := ""
		if b, ok := bindingByAdapter[name]; ok && strings.TrimSpace(b.Workspace) == currentWorkspace {
			targetID = util.FirstNonEmpty(b.TargetID, targetID)
			workspaceChannel = strings.TrimSpace(b.ChannelID)
		}
		entries = append(entries, imBindingEntry{
			Adapter:          name,
			Label:            name,
			TargetID:         targetID,
			WorkspaceChannel: workspaceChannel,
			OccupiedBy:       occupied[name],
			AdapterState:     imStatePtr(adapterStates[name]),
			Disabled:         !m.config.IM.Adapters[name].Enabled,
			Muted:            bindingByAdapter[name].Muted,
		})
	}
	return entries
}

func imBindingLabels(m *Model, entries []imBindingEntry, cfg imPanelConfig) []string {
	currentWS := m.currentWorkspacePath()
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		var status string
		switch {
		case entry.Disabled:
			status = m.t("panel." + cfg.adapterType + ".entry.disabled")
		case entry.Muted:
			status = m.t("panel." + cfg.adapterType + ".entry.muted")
		case entry.OccupiedBy != "" && entry.OccupiedBy == currentWS:
			status = m.t("panel." + cfg.adapterType + ".entry.active")
		case entry.OccupiedBy != "":
			status = m.t("panel."+cfg.adapterType+".entry.bound_other", entry.OccupiedBy)
		default:
			status = m.t("panel." + cfg.adapterType + ".entry.available")
		}
		labels = append(labels, fmt.Sprintf("%s · %s", entry.Adapter, status))
	}
	return labels
}

func imAdapterStatus(m *Model, state *im.AdapterState, cfg imPanelConfig) string {
	if state == nil {
		return m.t("panel." + cfg.adapterType + ".status.not_started")
	}
	status := strings.TrimSpace(state.Status)
	if status == "" {
		status = m.t("panel." + cfg.adapterType + ".status.unknown")
	}
	if state.Healthy {
		return status
	}
	return status
}

// createIMPanelAdapterCmd creates a command to add a new adapter
func (m *Model) createIMPanelAdapterCmd(cfg imPanelConfig, spec string) tea.Cmd {
	return func() tea.Msg {
		if m.config == nil {
			return imBindResultMsg{err: errors.New(m.t("panel." + cfg.adapterType + ".error.config_unavailable"))}
		}
		fields, err := cfg.parseCreateSpec(spec)
		if err != nil {
			return imBindResultMsg{err: errors.New(m.t("panel."+cfg.adapterType+".error.config_format") + ": " + err.Error())}
		}
		name := fields["name"]
		if name == "" {
			return imBindResultMsg{err: errors.New(m.t("panel." + cfg.adapterType + ".error.adapter_required"))}
		}
		adapter := cfg.createAdapter(&cfg, name, fields)
		m.config.IM.Enabled = true
		if err := m.config.AddIMAdapter(name, adapter); err != nil {
			return imBindResultMsg{err: err}
		}
		if err := m.ensureStartedCurrentWorkspaceIMRuntime(m.t("panel."+cfg.adapterType+".error.config_unavailable"), "", true); err != nil {
			return imBindResultMsg{err: err}
		}
		if err := m.startIMAdapterIfNeeded(cfg, name); err != nil {
			return imBindResultMsg{err: err}
		}
		return imBindResultMsg{message: m.t("panel."+cfg.adapterType+".message.added_bot", name)}
	}
}

// startIMAdapterIfNeeded starts an adapter if it's not already running
func (m *Model) startIMAdapterIfNeeded(cfg imPanelConfig, name string) error {
	if m.imManager == nil || m.config == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(m.t("panel." + cfg.adapterType + ".error.adapter_required"))
	}
	snapshot := m.imManager.Snapshot()
	for _, state := range snapshot.Adapters {
		if state.Name == name {
			return nil
		}
	}
	adapterCfg, ok := m.config.IM.Adapters[name]
	if !ok {
		return errors.New(m.t("panel."+cfg.adapterType+".error.not_configured", name))
	}
	if !adapterCfg.Enabled {
		// Auto-enable when user explicitly tries to bind from panel.
		if err := m.config.SetIMAdapterEnabled(name, true); err != nil {
			return fmt.Errorf("enable %s: %w", name, err)
		}
		if m.imManager != nil {
			_ = m.imManager.EnableBinding(name)
		}
	}
	if !strings.EqualFold(adapterCfg.Platform, string(cfg.platform)) {
		return errors.New(m.t("panel."+cfg.adapterType+".error.not_"+cfg.adapterType+"_adapter", name))
	}
	return im.StartNamedAdapter(context.Background(), m.config.IM, name, m.imManager)
}

// bindIMPanelEntry binds a panel entry to the current workspace
func (m *Model) bindIMPanelEntry(cfg imPanelConfig, entry imBindingEntry) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureIMPanelBotBinding(cfg, entry.Adapter); err != nil {
			return imBindResultMsg{err: err}
		}
		if m.agent != nil {
			if err := waitForIMAdapterHealthy(m, m.imManager, entry.Adapter, 10*time.Second); err != nil {
				return imBindResultMsg{err: err}
			}
			// Sync session history to the newly bound channel only.
			if binding := m.imManager.Snapshot().BindingByAdapter(entry.Adapter); binding != nil {
				if err := m.imManager.SyncSessionHistory(context.Background(), *binding, m.agent.Messages()); err != nil && err != im.ErrNoChannelBound {
					return imBindResultMsg{err: err}
				}
			}
		}
		return imBindResultMsg{message: m.t("panel." + cfg.adapterType + ".message.bound_success")}
	}
}

// unbindIMPanelEntry unbinds a panel entry from the current workspace
func (m *Model) unbindIMPanelEntry(cfg imPanelConfig, adapterName string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureStartedCurrentWorkspaceIMRuntime(m.t("panel."+cfg.adapterType+".error.config_unavailable"), "", true); err != nil {
			return imBindResultMsg{err: err}
		}
		if err := m.imManager.UnbindAdapter(adapterName); err != nil {
			return imBindResultMsg{err: err}
		}
		return imBindResultMsg{message: m.t("panel." + cfg.adapterType + ".message.unbound")}
	}
}

// clearIMPanelChannel clears the channel for a panel entry
func (m *Model) clearIMPanelChannel(cfg imPanelConfig, adapterName string) tea.Cmd {
	return func() tea.Msg {
		if err := m.ensureStartedCurrentWorkspaceIMRuntime(m.t("panel."+cfg.adapterType+".error.config_unavailable"), "", true); err != nil {
			return imBindResultMsg{err: err}
		}
		if err := m.imManager.ClearChannelByAdapter(adapterName); err != nil {
			return imBindResultMsg{err: err}
		}
		return imBindResultMsg{message: m.t("panel." + cfg.adapterType + ".message.cleared")}
	}
}

// ensureIMPanelBotBinding ensures a bot is bound to the current workspace
func (m *Model) ensureIMPanelBotBinding(cfg imPanelConfig, adapter string) error {
	if err := m.ensureStartedCurrentWorkspaceIMRuntime(m.t("panel."+cfg.adapterType+".error.config_unavailable"), "", true); err != nil {
		return err
	}
	if err := m.startIMAdapterIfNeeded(cfg, adapter); err != nil {
		return err
	}
	workspace := m.currentWorkspacePath()
	for _, b := range currentIMBindings(m.imManager, cfg.platform) {
		if strings.TrimSpace(b.Workspace) == strings.TrimSpace(workspace) && b.Adapter == adapter {
			return nil
		}
	}
	_, err := m.imManager.BindChannel(im.ChannelBinding{
		Platform: cfg.platform,
		Adapter:  adapter,
		TargetID: defaultIMTargetID(workspace),
	})
	return err
}

// generateIMPanelShareLink generates a share link for an adapter (used by QQ and potentially others)
func (m *Model) generateIMPanelShareLink(entry imBindingEntry) tea.Cmd {
	return func() tea.Msg {
		workspace := m.currentWorkspacePath()
		callbackData := defaultIMTargetID(workspace)
		if len(callbackData) > 32 {
			return imBindResultMsg{err: errors.New("workspace path too long for share link")}
		}
		link, err := m.imManager.GenerateShareLink(context.Background(), entry.Adapter, callbackData)
		if err != nil {
			return imBindResultMsg{err: err}
		}
		qr, err := renderCompactTerminalQRCode(link)
		if err != nil {
			return imBindResultMsg{err: fmt.Errorf("render QR code: %w", err)}
		}
		return imBindResultMsg{
			message:      m.t("panel." + entry.Adapter + ".message.share_generated"),
			shareAdapter: entry.Adapter,
			shareLink:    link,
			shareQRCode:  qr,
		}
	}
}
