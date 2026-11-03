package wailskit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/session"
)

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
	Workspace string                 `json:"workspace,omitempty"`
	IsCurrent bool                   `json:"isCurrent"`
}

type IMPlatformMeta struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName"`
	Fields      []IMPlatformField `json:"fields"`
	QRAuth      bool              `json:"qrAuth"`
}

type IMPlatformField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Secret      bool   `json:"secret,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

func GetIMPlatformRegistry() []IMPlatformMeta {
	return []IMPlatformMeta{

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

		{ID: "privateclaw", DisplayName: "Private Claw", Fields: []IMPlatformField{}},
	}
}

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

func firstBoundWorkspace(bindings []string, workingDir, normalizedWS string) (string, bool) {
	isCurrent := false
	first := ""
	for _, ws := range bindings {
		if ws != "" && (ws == workingDir || ws == normalizedWS) {

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

	boundWorkspaces := make(map[string][]string)
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

func SaveIMAdapter(name string, values map[string]string) error {

	globalMu.Lock()
	defer globalMu.Unlock()
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	platform := values["platform"]
	if platform == "" {
		return fmt.Errorf("platform is required")
	}

	meta := imPlatformByID(platform)
	if meta == nil {
		return fmt.Errorf("unknown platform %q (see GetIMPlatformRegistry for supported IDs)", platform)
	}

	platform = meta.ID

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

	extra := make(map[string]interface{})
	for k, v := range values {
		switch k {
		case "platform", "transport", "command", "enabled":

		default:
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		adapterCfg.Extra = extra
	}

	if existing, exists := cfg.IM.Adapters[name]; exists {

		cfg.IM.Adapters[name] = mergeExistingIntoUpdate(adapterCfg, existing)
	}

	if _, exists := cfg.IM.Adapters[name]; exists {
		return cfg.Save()
	}
	return cfg.AddIMAdapter(name, adapterCfg)
}

func mergeExistingIntoUpdate(update, existing config.IMAdapterConfig) config.IMAdapterConfig {
	update.OutputMode = existing.OutputMode
	update.Targets = existing.Targets

	samePlatform := strings.EqualFold(strings.TrimSpace(existing.Platform), strings.TrimSpace(update.Platform))
	if samePlatform {
		update.Args = existing.Args
		update.Env = existing.Env
		update.AllowFrom = existing.AllowFrom
		if update.Transport == "" {
			update.Transport = existing.Transport
		}
		if update.Command == "" {
			update.Command = existing.Command
		}
	}
	if existing.Extra != nil && samePlatform {
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

func RemoveIMAdapter(name string, imMgr interface {
	UnbindAdapter(adapterName string) error
}) error {

	globalMu.Lock()
	defer globalMu.Unlock()
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if imMgr != nil {
		if uerr := imMgr.UnbindAdapter(name); uerr != nil {

			return fmt.Errorf("unbinding channels failed (adapter config left intact, safe to retry): %w", uerr)
		}
	}
	if err := cfg.RemoveIMAdapter(name); err != nil {

		return err
	}
	return nil
}

func SetIMAdapterEnabled(name string, enabled bool) error {

	globalMu.Lock()
	defer globalMu.Unlock()
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return cfg.SetIMAdapterEnabled(name, enabled)
}

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

	platformMeta := imPlatformByID(acfg.Platform)
	if platformMeta == nil {
		return fmt.Errorf("unknown platform %q for adapter %q", acfg.Platform, name)
	}

	for _, field := range platformMeta.Fields {
		if !field.Required {
			continue
		}
		val, ok := acfg.Extra[field.Key]
		if !ok {
			return fmt.Errorf("missing required field %q (%s)", field.Key, field.Label)
		}

		if val == nil {
			return fmt.Errorf("required field %q (%s) is empty", field.Key, field.Label)
		}

		strVal := fmt.Sprintf("%v", val)
		if strVal == "" || strVal == "<nil>" {
			return fmt.Errorf("required field %q (%s) is empty or invalid", field.Key, field.Label)
		}
	}

	if acfg.Transport == "stdio" && strings.TrimSpace(acfg.Command) == "" {
		return fmt.Errorf("missing required field %q (stdio command)", "command")
	}

	return nil
}

func BindIMAdapter(name, workingDir string, imMgr interface{ BindAdapterToWorkspace(string, string) error }) error {
	if imMgr == nil {

		return fmt.Errorf("IM manager not initialized")
	}

	if err := imAdapterExistsInConfig(name); err != nil {
		return err
	}

	workspace := session.NormalizeWorkspacePath(workingDir)
	return imMgr.BindAdapterToWorkspace(name, workspace)
}

func RebindIMAdapter(name, workingDir string, imMgr interface{ BindAdapterToWorkspace(string, string) error }) error {
	if imMgr == nil {

		return fmt.Errorf("IM manager not initialized")
	}

	if err := imAdapterExistsInConfig(name); err != nil {
		return err
	}

	workspace := session.NormalizeWorkspacePath(workingDir)
	return imMgr.BindAdapterToWorkspace(name, workspace)
}

func UnbindIMAdapter(name string, imMgr interface{ UnbindAdapter(string) error }) error {
	if imMgr == nil {
		return fmt.Errorf("IM manager not initialized")
	}
	return imMgr.UnbindAdapter(name)
}

func imAdapterExistsInConfig(name string) error {
	cfg, err := config.Load(config.ConfigPath())
	if err != nil {

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
