package wailskit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
)

// IMAdapterInfo is a frontend-friendly representation of an IM adapter config.
type IMAdapterInfo struct {
	Name      string                 `json:"name"`
	Enabled   bool                   `json:"enabled"`
	Muted     bool                   `json:"muted"`
	Platform  string                 `json:"platform"`
	Transport string                 `json:"transport"`
	Command   string                 `json:"command"`
	Args      []string               `json:"args,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
	Targets   []string               `json:"targets,omitempty"`
	Workspace string                 `json:"workspace,omitempty"` // bound workspace path
	IsCurrent bool                   `json:"isCurrent"`           // bound to current workspace
}

// IMPlatformMeta describes a supported IM platform for the frontend.
type IMPlatformMeta struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName"`
	Fields      []IMPlatformField `json:"fields"`
	QRAuth      bool              `json:"qrAuth"`
}

// IMPlatformField describes a configuration field for an IM platform.
// #637: Required distinguishes credentials the adapter cannot start without
// from optional fields with runtime defaults. Previously every registry
// field carried a Label and the validator's `Label == "" → skip` branch was
// dead code, so optional fields (signal base_url, irc channels) were
// hard-blocked as required.
type IMPlatformField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Secret      bool   `json:"secret,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// GetIMPlatformRegistry returns the list of supported IM platforms.
func GetIMPlatformRegistry() []IMPlatformMeta {
	return []IMPlatformMeta{
		// #637: Required flags preserve the pre-existing validation strength
		// for real credentials; only runtime-optional fields (signal base_url,
		// irc channels) stay unflagged.
		{ID: "qq", DisplayName: "QQ", Fields: []IMPlatformField{{Key: "appid", Label: "App ID", Placeholder: "QQ app ID", Required: true}, {Key: "appsecret", Label: "App Secret", Placeholder: "QQ app secret", Secret: true, Required: true}}},
		{ID: "telegram", DisplayName: "Telegram", Fields: []IMPlatformField{{Key: "bot_token", Label: "Bot Token", Placeholder: "123456:ABC-DEF...", Secret: true, Required: true}}},
		{ID: "discord", DisplayName: "Discord", Fields: []IMPlatformField{{Key: "token", Label: "Bot Token", Placeholder: "Discord bot token", Secret: true, Required: true}}},
		{ID: "feishu", DisplayName: "Feishu", Fields: []IMPlatformField{{Key: "app_id", Label: "App ID", Placeholder: "cli_xxx", Required: true}, {Key: "app_secret", Label: "App Secret", Placeholder: "Feishu app secret", Secret: true, Required: true}}},
		{ID: "dingtalk", DisplayName: "DingTalk", Fields: []IMPlatformField{{Key: "app_key", Label: "App Key", Placeholder: "dingxxx", Required: true}, {Key: "app_secret", Label: "App Secret", Placeholder: "DingTalk app secret", Secret: true, Required: true}}},
		{ID: "slack", DisplayName: "Slack", Fields: []IMPlatformField{{Key: "bot_token", Label: "Bot Token", Placeholder: "xoxb-xxx", Secret: true, Required: true}, {Key: "app_token", Label: "App Token", Placeholder: "xapp-xxx", Secret: true, Required: true}}},
		{ID: "wechat", DisplayName: "WeChat", Fields: []IMPlatformField{}, QRAuth: true},
		{ID: "wecom", DisplayName: "WeCom", Fields: []IMPlatformField{{Key: "bot_id", Label: "Bot ID", Placeholder: "WeCom bot ID", Required: true}, {Key: "secret", Label: "Secret", Placeholder: "WeCom secret", Secret: true, Required: true}}},
		{ID: "whatsapp", DisplayName: "WhatsApp", Fields: []IMPlatformField{}, QRAuth: true},
		{ID: "mattermost", DisplayName: "Mattermost", Fields: []IMPlatformField{{Key: "url", Label: "Server URL", Placeholder: "https://mm.example.com", Required: true}, {Key: "token", Label: "Access Token", Placeholder: "mattermost token", Secret: true, Required: true}}},
		{ID: "signal", DisplayName: "Signal", Fields: []IMPlatformField{{Key: "account", Label: "Phone Number", Placeholder: "+1234567890", Required: true}, {Key: "base_url", Label: "Signal CLI URL", Placeholder: "http://localhost:8080"}}},
		{ID: "irc", DisplayName: "IRC", Fields: []IMPlatformField{{Key: "host", Label: "Server", Placeholder: "irc.libera.chat:6697", Required: true}, {Key: "nick", Label: "Nickname", Placeholder: "my-bot", Required: true}, {Key: "channels", Label: "Channels", Placeholder: "#channel1,#channel2"}}},
		// #591: privateclaw is listed in the CLI help/platform docs (im_cmd.go
		// accepts it verbatim), but was absent here — after #585's strong
		// validation, doc-compliant adapters permanently failed Test
		// Connection with "unknown platform".
		{ID: "privateclaw", DisplayName: "Private Claw", Fields: []IMPlatformField{}},
	}
}

// imPlatformByID returns the registry meta for a platform ID,
// case-insensitively (#591): hand-written YAML and CLI input can carry
// "Telegram" where the registry says "telegram" — a case mismatch must
// not turn into "unknown platform".
func imPlatformByID(platform string) *IMPlatformMeta {
	registry := GetIMPlatformRegistry()
	lower := strings.ToLower(platform)
	for i := range registry {
		if strings.ToLower(registry[i].ID) == lower {
			return &registry[i]
		}
	}
	return nil
}

// firstBoundWorkspace picks the workspace shown for an adapter from its
// binding list: the current-workspace match if present (so the UI shows the
// binding that matters here), else the first binding. IsCurrent is true iff
// any binding matches the current workspace (#587).
func firstBoundWorkspace(bindings []string, workingDir, normalizedWS string) (string, bool) {
	isCurrent := false
	first := ""
	for _, ws := range bindings {
		if ws != "" && (ws == workingDir || ws == normalizedWS) {
			// #591: the ws != "" guard from the pre-#587 code was dropped in the
			// rewrite — with workingDir=="" (first start, uncached) and an
			// empty-workspace legacy binding, both-empty compared equal and
			// IsCurrent flipped true, nudging users to skip a needed bind.
			// Empty workspace never matches; also guard the return value.
			if workingDir != "" || normalizedWS != "" {
				return ws, true
			}
		}
		if first == "" && ws != "" {
			first = ws
		}
	}
	return first, isCurrent
}

// ListIMAdapters returns all configured IM adapters with workspace binding info.
// imManager may be nil (no runtime bindings available).
func ListIMAdapters(workingDir string, imMgr interface {
	AllPersistedBindings() []im.ChannelBinding
	IsMuted(adapterName string) bool
}) ([]IMAdapterInfo, error) {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.IM.Adapters == nil {
		return nil, nil
	}

	normalizedWS := session.NormalizeWorkspacePath(workingDir)

	// Collect workspace bindings from imManager if available.
	// #587: an adapter can carry MULTIPLE persisted bindings (legacy
	// orphans from before #396's cascade cleanup + the current-workspace
	// binding). Folding them into map[string]string silently kept only the
	// slice's last entry (last-write-wins), so IsCurrent could read false
	// even though the current workspace IS bound — misleading users into
	// redundant re-binds. Keep all bound workspaces; IsCurrent matches on
	// ANY binding.
	boundWorkspaces := make(map[string][]string) // adapterName → bound workspaces
	if imMgr != nil {
		for _, b := range imMgr.AllPersistedBindings() {
			if b.Adapter != "" {
				boundWorkspaces[b.Adapter] = append(boundWorkspaces[b.Adapter], b.Workspace)
			}
		}
	}

	var result []IMAdapterInfo
	for name, acfg := range cfg.IM.Adapters {
		var isCurrent bool
		ws, isCurrent := firstBoundWorkspace(boundWorkspaces[name], workingDir, normalizedWS)

		var muted bool
		if imMgr != nil {
			muted = imMgr.IsMuted(name)
		}

		result = append(result, IMAdapterInfo{
			Name:      name,
			Enabled:   acfg.Enabled,
			Muted:     muted,
			Platform:  acfg.Platform,
			Transport: acfg.Transport,
			Command:   acfg.Command,
			Args:      acfg.Args,
			Extra:     acfg.Extra,
			Workspace: ws,
			IsCurrent: isCurrent,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// SaveIMAdapter creates or updates an IM adapter. The cfg map may contain:
//   - "platform" (required): adapter platform (e.g. "telegram", "discord", "slack")
//   - "transport": transport type
//   - "command": command for stdio transport
//   - other keys are stored in the adapter's Extra map
func SaveIMAdapter(name string, values map[string]string) error {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	platform := values["platform"]
	if platform == "" {
		return fmt.Errorf("platform is required")
	}
	// #637: reject unknown platforms at SAVE time, not just Test time.
	// AddIMAdapter only checks non-empty, so "telegarm" persisted happily
	// and the user only discovered the typo when Test Connection failed
	// with "unknown platform" — leaving unstartable bad data in ggcode.yaml.
	if imPlatformByID(platform) == nil {
		return fmt.Errorf("unknown platform %q (see GetIMPlatformRegistry for supported IDs)", platform)
	}

	// Read enabled value from values map. If the caller did not send an
	// explicit enabled value, preserve the existing adapter's Enabled state
	// (fix #155: editing other fields must not silently re-enable a
	// disabled adapter).
	enabled := true
	if existing, exists := cfg.IM.Adapters[name]; exists {
		enabled = existing.Enabled
	}
	switch values["enabled"] {
	case "true":
		enabled = true
	case "false":
		enabled = false
	}

	adapterCfg := config.IMAdapterConfig{
		Enabled:   enabled,
		Platform:  platform,
		Transport: values["transport"],
		Command:   values["command"],
	}

	// Collect remaining keys as Extra
	extra := make(map[string]interface{})
	for k, v := range values {
		switch k {
		case "platform", "transport", "command", "enabled":
			// handled separately
		default:
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		adapterCfg.Extra = extra
	}

	// Check if updating an existing adapter
	if existing, exists := cfg.IM.Adapters[name]; exists {
		// Update the adapter in-place and save once (avoid delete→save→re-add data loss).
		cfg.IM.Adapters[name] = mergeExistingIntoUpdate(adapterCfg, existing)
	}

	// If the adapter didn't exist above, AddIMAdapter creates it. If it did,
	// we already updated it in-memory — just persist.
	if _, exists := cfg.IM.Adapters[name]; exists {
		return cfg.Save()
	}
	return cfg.AddIMAdapter(name, adapterCfg)
}

// mergeExistingIntoUpdate folds an existing adapter config into a UI update
// payload: fields the desktop payload never carries are preserved (#107;
// transport/command per #300), and Extra keys absent from the update are
// kept — but ONLY when the platform is unchanged. On a platform switch the
// old Extra is discarded to prevent cross-platform credential leakage
// (#585): telegram and slack share the "bot_token" field name, so a switch
// would otherwise inherit the old token as the new platform's credential.
func mergeExistingIntoUpdate(update, existing config.IMAdapterConfig) config.IMAdapterConfig {
	update.Args = existing.Args
	update.Env = existing.Env
	update.AllowFrom = existing.AllowFrom
	if update.Transport == "" {
		update.Transport = existing.Transport
	}
	if update.Command == "" {
		update.Command = existing.Command
	}
	update.OutputMode = existing.OutputMode
	update.Targets = existing.Targets
	if existing.Extra != nil && existing.Platform == update.Platform {
		if update.Extra == nil {
			update.Extra = make(map[string]interface{})
		}
		for k, v := range existing.Extra {
			if _, inUpdate := update.Extra[k]; !inUpdate {
				update.Extra[k] = v
			}
		}
	}
	return update
}

// RemoveIMAdapter removes an IM adapter by name. When imMgr is non-nil the
// adapter's persisted bindings are cascade-deleted in the same operation
// (#396): without the cascade an orphaned binding keyed by adapter NAME is
// silently re-inherited when an adapter with the same name is rebuilt later.
//
// Ordering (#497): the cascade unbind runs BEFORE the config delete.
// im.Manager.UnbindAdapter is idempotent (no persisted bindings is a
// successful no-op), so this order keeps BOTH failure paths retryable: if
// the unbind fails the adapter config is still intact, and if the config
// delete fails the retry's unbind is a harmless no-op. The previous
// delete-then-unbind order made the #460 "retry" advice structurally
// unreachable — config.RemoveIMAdapter rejects the already-deleted adapter
// with "not found" before a retry ever reaches the unbind step.
func RemoveIMAdapter(name string, imMgr interface {
	UnbindAdapter(adapterName string) error
}) error {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if imMgr != nil {
		if uerr := imMgr.UnbindAdapter(name); uerr != nil {
			// #460: surface the cascade failure so the user can retry; the
			// config delete below has NOT run yet, so a retry re-enters the
			// full chain instead of dying at "adapter not found".
			return fmt.Errorf("unbinding channels failed (adapter config left intact, safe to retry): %w", uerr)
		}
	}
	if err := cfg.RemoveIMAdapter(name); err != nil {
		// Bindings are already cleared by the idempotent unbind above, so a
		// retry that re-runs this function cannot leak a ghost binding (#396).
		return err
	}
	return nil
}

// SetIMAdapterEnabled toggles the enabled state of an IM adapter.
func SetIMAdapterEnabled(name string, enabled bool) error {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return cfg.SetIMAdapterEnabled(name, enabled)
}

// TestIMConnection performs basic required-field validation for an IM adapter.
// It checks that all required fields for the platform are non-empty.
// Note: this is field validation only — full connectivity and authentication
// requires starting the actual adapter runtime (im.Manager).
func TestIMConnection(name string) error {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	acfg, ok := cfg.IM.Adapters[name]
	if !ok {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	if acfg.Platform == "" {
		return fmt.Errorf("adapter %q has no platform configured", name)
	}

	// Validate required fields from platform registry
	platformMeta := imPlatformByID(acfg.Platform)
	if platformMeta == nil {
		return fmt.Errorf("unknown platform %q for adapter %q", acfg.Platform, name)
	}

	// Check required fields (registry marks which are mandatory).
	for _, field := range platformMeta.Fields {
		if !field.Required {
			continue
		}
		val, ok := acfg.Extra[field.Key]
		if !ok {
			return fmt.Errorf("missing required field %q (%s)", field.Key, field.Label)
		}
		// Normalize to string. #591: a hand-written ggcode.yaml like
		// `appid: 123456789` parses as int (yaml.v3), and the previous
		// val.(string) assertion misreported a populated field as "empty
		// or invalid", sending users hunting for a problem that wasn't
		// there. fmt %v keeps string values verbatim.
		strVal := fmt.Sprintf("%v", val)
		if strVal == "" {
			return fmt.Errorf("required field %q (%s) is empty or invalid", field.Key, field.Label)
		}
	}

	// #637: a stdio adapter's essential Command lives on the adapter struct
	// (adapterCfg.Command), not in Extra — the Extra-only loop above never
	// checked it, so a Command-less stdio config passed field validation.
	if acfg.Transport == "stdio" && strings.TrimSpace(acfg.Command) == "" {
		return fmt.Errorf("missing required field %q (stdio command)", "command")
	}

	return nil
}

// BindIMAdapter binds an adapter to the current workspace.
// imMgr may be nil (adapter will be bound but won't be active until manager is initialized).
func BindIMAdapter(name, workingDir string, imMgr interface{ BindAdapterToWorkspace(string, string) error }) error {
	if imMgr == nil {
		// Adapter will be bound but not yet active; will take effect on next startup
		return fmt.Errorf("IM manager not initialized")
	}

	// #556: validate the adapter exists in config BEFORE binding. Without this
	// check the binding persisted to disk for an adapter that has no config
	// entry — a "ghost" binding invisible in the UI, leaving state desynced.
	if err := imAdapterExistsInConfig(name); err != nil {
		return err
	}

	workspace := session.NormalizeWorkspacePath(workingDir)
	return imMgr.BindAdapterToWorkspace(name, workspace)
}

// RebindIMAdapter re-binds an adapter to the current workspace,
// replacing any existing binding to another workspace.
// imMgr may be nil (adapter will be rebound but won't be active until manager is initialized).
func RebindIMAdapter(name, workingDir string, imMgr interface{ BindAdapterToWorkspace(string, string) error }) error {
	if imMgr == nil {
		// Adapter will be rebound but not yet active; will take effect on next startup
		return fmt.Errorf("IM manager not initialized")
	}

	// #556: same ghost-binding guard as BindIMAdapter.
	if err := imAdapterExistsInConfig(name); err != nil {
		return err
	}

	workspace := session.NormalizeWorkspacePath(workingDir)
	return imMgr.BindAdapterToWorkspace(name, workspace)
}

// UnbindIMAdapter removes all bindings for an adapter.
func UnbindIMAdapter(name string, imMgr interface{ UnbindAdapter(string) error }) error {
	if imMgr == nil {
		return fmt.Errorf("IM manager not initialized")
	}
	return imMgr.UnbindAdapter(name)
}

// imAdapterExistsInConfig verifies that the named adapter has a config entry
// in the user's ggcode.yaml (#556). Binding an adapter that is absent from
// config persists a binding the UI cannot show (no adapter list entry),
// producing a ghost binding on disk.
func imAdapterExistsInConfig(name string) error {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		// Config unreadable: do not block binding on a load failure — the
		// manager may still be able to resolve the adapter at runtime.
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found in config (add it in IM settings before binding)", name)
	}
	if _, ok := cfg.IM.Adapters[name]; !ok {
		return fmt.Errorf("IM adapter %q not found in config (add it in IM settings before binding)", name)
	}
	return nil
}
