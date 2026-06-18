package config

import (
	"github.com/topcheer/ggcode/internal/hooks"
	"gopkg.in/yaml.v3"
)

// marshalInstanceDelta computes the minimal set of top-level fields that
// differ between the current Config and the globalSnap (original global values).
// Returns a raw map containing only the changed fields, suitable for yaml.Marshal.
//
// The comparison is field-by-field: a field is included in the delta only if
// the current value differs from the globalSnap value. This ensures the instance
// file is always the smallest possible representation of the overrides.
func (c *Config) marshalInstanceDelta() map[string]interface{} {
	delta := map[string]interface{}{}

	if c.globalSnap == nil {
		// No global snapshot — try to load the global config file as baseline.
		if c.FilePath != "" {
			if globalCfg, err := Load(c.FilePath); err == nil {
				c.globalSnap = globalCfg
			}
		}
		if c.globalSnap == nil {
			// No global file either. This happens when SaveInstance is called on
			// a manually-constructed Config (e.g. tests, or config set via slash
			// commands before any file exists). Use an empty Config as the baseline
			// so all non-zero fields are included in the delta.
			c.globalSnap = &Config{}
		}
	}

	// Compare each scalar field.
	c.diffScalar("vendor", c.Vendor, c.globalSnap.Vendor, delta)
	c.diffScalar("endpoint", c.Endpoint, c.globalSnap.Endpoint, delta)
	c.diffScalar("model", c.Model, c.globalSnap.Model, delta)
	c.diffScalar("language", c.Language, c.globalSnap.Language, delta)
	c.diffScalar("system_prompt", c.ExtraPrompt, c.globalSnap.ExtraPrompt, delta)
	c.diffScalar("default_mode", c.DefaultMode, c.globalSnap.DefaultMode, delta)
	c.diffInt("max_iterations", c.MaxIterations, c.globalSnap.MaxIterations, delta)

	// UI
	c.diffUI(&c.UI, &c.globalSnap.UI, delta)

	// IM — only write the whole im block if something changed
	c.diffIM(&c.IM, &c.globalSnap.IM, delta)

	// Vendors — only include vendors that exist in current but not in globalSnap
	c.diffVendors(c.Vendors, c.globalSnap.Vendors, delta)

	// A2A
	c.diffA2A(&c.A2A, &c.globalSnap.A2A, delta)

	// Knight
	c.diffKnight(&c.KnightConfig, &c.globalSnap.KnightConfig, delta)

	// Impersonation
	if c.Impersonation.Preset != c.globalSnap.Impersonation.Preset ||
		c.Impersonation.CustomVersion != c.globalSnap.Impersonation.CustomVersion ||
		len(c.Impersonation.CustomHeaders) != len(c.globalSnap.Impersonation.CustomHeaders) {
		impMap := map[string]interface{}{}
		if c.Impersonation.Preset != "" {
			impMap["preset"] = c.Impersonation.Preset
		}
		if c.Impersonation.CustomVersion != "" {
			impMap["custom_version"] = c.Impersonation.CustomVersion
		}
		if len(c.Impersonation.CustomHeaders) > 0 {
			impMap["custom_headers"] = c.Impersonation.CustomHeaders
		}
		delta["impersonation"] = impMap
	}

	// AllowedDirs
	c.diffStringSlice("allowed_dirs", c.AllowedDirs, c.globalSnap.AllowedDirs, delta)

	// ToolPerms — only include tool permissions that exist in current but not globalSnap
	c.diffToolPerms(c.ToolPerms, c.globalSnap.ToolPerms, delta)

	// MCPServers
	c.diffSlice("mcp_servers", c.MCPServers, c.globalSnap.MCPServers, delta)

	// Plugins
	c.diffSlice("plugins", c.Plugins, c.globalSnap.Plugins, delta)

	// SubAgents
	c.diffSubAgents(&c.SubAgents, &c.globalSnap.SubAgents, delta)

	// Swarm
	c.diffSwarm(&c.Swarm, &c.globalSnap.Swarm, delta)

	// Hooks
	c.diffHooks(&c.Hooks, &c.globalSnap.Hooks, delta)

	return delta
}

// --- diff helpers ---

func (*Config) diffScalar(key, current, global string, delta map[string]interface{}) {
	if current != global && current != "" {
		delta[key] = current
	}
}

func (*Config) diffInt(key string, current, global int, delta map[string]interface{}) {
	if current != global && current != 0 {
		delta[key] = current
	}
}

func (*Config) diffStringSlice(key string, current, global []string, delta map[string]interface{}) {
	if len(current) == 0 {
		return
	}
	if len(global) != len(current) {
		delta[key] = current
		return
	}
	match := true
	for i := range current {
		if current[i] != global[i] {
			match = false
			break
		}
	}
	if !match {
		delta[key] = current
	}
}

func (*Config) diffSlice(key string, current, global interface{}, delta map[string]interface{}) {
	// Use YAML round-trip to compare — only include if different.
	cData, _ := yaml.Marshal(current)
	gData, _ := yaml.Marshal(global)
	if len(cData) > 0 && string(cData) != string(gData) {
		delta[key] = current
	}
}

func (*Config) diffUI(current, global *UIConfig, delta map[string]interface{}) {
	uiDelta := map[string]interface{}{}

	// SidebarVisible: compare pointer values
	curVal := current.SidebarVisible != nil && *current.SidebarVisible
	gloVal := global.SidebarVisible != nil && *global.SidebarVisible
	if current.SidebarVisible != nil && (global.SidebarVisible == nil || curVal != gloVal) {
		uiDelta["sidebar_visible"] = curVal
	}

	if len(uiDelta) > 0 {
		delta["ui"] = uiDelta
	}
}

func (cfg *Config) diffIM(current, global *IMConfig, delta map[string]interface{}) {
	imDelta := map[string]interface{}{}

	if current.Enabled != global.Enabled {
		imDelta["enabled"] = current.Enabled
	}
	if current.ActiveSessionPolicy != global.ActiveSessionPolicy && current.ActiveSessionPolicy != "" {
		imDelta["active_session_policy"] = current.ActiveSessionPolicy
	}
	// RequireLocalSession
	if current.RequireLocalSession != nil && (global.RequireLocalSession == nil ||
		*current.RequireLocalSession != *global.RequireLocalSession) {
		imDelta["require_local_session"] = *current.RequireLocalSession
	}
	if current.OutputMode != global.OutputMode && current.OutputMode != "" {
		imDelta["output_mode"] = current.OutputMode
	}
	// Streaming
	if current.Streaming.Enabled != global.Streaming.Enabled {
		streamDelta := map[string]interface{}{}
		streamDelta["enabled"] = current.Streaming.Enabled
		imDelta["streaming"] = streamDelta
	}
	// STT
	if current.STT.Provider != global.STT.Provider && current.STT.Provider != "" {
		sttDelta := map[string]interface{}{}
		sttDelta["provider"] = current.STT.Provider
		imDelta["stt"] = sttDelta
	}
	// Adapters — only include adapters that exist in current but not in global
	for k, v := range current.Adapters {
		if _, exists := global.Adapters[k]; !exists {
			adapterMap := map[string]interface{}{
				"platform": v.Platform,
				"enabled":  v.Enabled,
			}
			if len(v.Targets) > 0 {
				adapterMap["targets"] = v.Targets
			}
			if len(v.Extra) > 0 {
				adapterMap["extra"] = v.Extra
			}
			if imDelta["adapters"] == nil {
				imDelta["adapters"] = map[string]interface{}{}
			}
			imDelta["adapters"].(map[string]interface{})[k] = adapterMap
		}
	}

	if len(imDelta) > 0 {
		delta["im"] = imDelta
	}
}

func (*Config) diffVendors(current, global map[string]VendorConfig, delta map[string]interface{}) {
	if len(current) == 0 {
		return
	}
	vendorDelta := map[string]interface{}{}
	for k, v := range current {
		gv, exists := global[k]
		if !exists {
			// Vendor only in current — include it
			vendorDelta[k] = v
			continue
		}
		// Vendor exists in both — check if they differ
		cData, _ := yaml.Marshal(v)
		gData, _ := yaml.Marshal(gv)
		if string(cData) != string(gData) {
			vendorDelta[k] = v
		}
	}
	if len(vendorDelta) > 0 {
		delta["vendors"] = vendorDelta
	}
}

func (*Config) diffA2A(current, global *A2AConfig, delta map[string]interface{}) {
	a2aDelta := map[string]interface{}{}

	if current.Disabled != global.Disabled {
		a2aDelta["disabled"] = current.Disabled
	}
	if current.Port != global.Port && current.Port != 0 {
		a2aDelta["port"] = current.Port
	}
	if current.Host != global.Host && current.Host != "" {
		a2aDelta["host"] = current.Host
	}
	if current.Auth.APIKey != global.Auth.APIKey && current.Auth.APIKey != "" {
		a2aDelta["auth"] = map[string]any{"api_key": current.Auth.APIKey}
	}
	if current.MaxTasks != global.MaxTasks && current.MaxTasks != 0 {
		a2aDelta["max_tasks"] = current.MaxTasks
	}
	if current.TaskTimeout != global.TaskTimeout && current.TaskTimeout != "" {
		a2aDelta["task_timeout"] = current.TaskTimeout
	}
	if current.LANDiscovery != global.LANDiscovery {
		a2aDelta["lan_discovery"] = current.LANDiscovery
	}
	// Auth
	authDelta := map[string]interface{}{}
	if current.Auth.APIKey != global.Auth.APIKey && current.Auth.APIKey != "" {
		authDelta["api_key"] = current.Auth.APIKey
	}
	if len(current.Auth.APIKeys) > 0 && len(global.Auth.APIKeys) == 0 {
		authDelta["api_keys"] = current.Auth.APIKeys
	}
	if current.Auth.OAuth2 != nil && global.Auth.OAuth2 == nil {
		authDelta["oauth2"] = current.Auth.OAuth2
	}
	if current.Auth.OIDC != nil && global.Auth.OIDC == nil {
		authDelta["oidc"] = current.Auth.OIDC
	}
	if current.Auth.MTLS != nil && global.Auth.MTLS == nil {
		authDelta["mtls"] = current.Auth.MTLS
	}
	if len(authDelta) > 0 {
		a2aDelta["auth"] = authDelta
	}

	if len(a2aDelta) > 0 {
		delta["a2a"] = a2aDelta
	}
}

func (*Config) diffKnight(current, global *KnightConfig, delta map[string]interface{}) {
	knightDelta := map[string]interface{}{}

	if current.Enabled != global.Enabled {
		knightDelta["enabled"] = current.Enabled
	}
	if current.TrustLevel != global.TrustLevel && current.TrustLevel != "" {
		knightDelta["trust_level"] = current.TrustLevel
	}
	if current.DailyTokenBudget != global.DailyTokenBudget && current.DailyTokenBudget != 0 {
		knightDelta["daily_token_budget"] = current.DailyTokenBudget
	}
	if current.IdleDelaySec != global.IdleDelaySec && current.IdleDelaySec != 0 {
		knightDelta["idle_delay_sec"] = current.IdleDelaySec
	}
	if len(current.Capabilities) > 0 && len(global.Capabilities) == 0 {
		knightDelta["capabilities"] = current.Capabilities
	}
	if current.Vendor != global.Vendor && current.Vendor != "" {
		knightDelta["vendor"] = current.Vendor
	}
	if current.Endpoint != global.Endpoint && current.Endpoint != "" {
		knightDelta["endpoint"] = current.Endpoint
	}
	if current.Model != global.Model && current.Model != "" {
		knightDelta["model"] = current.Model
	}

	if len(knightDelta) > 0 {
		delta["knight"] = knightDelta
	}
}

func (*Config) diffToolPerms(current, global map[string]ToolPermission, delta map[string]interface{}) {
	if len(current) == 0 {
		return
	}
	permDelta := map[string]interface{}{}
	for k, v := range current {
		if _, exists := global[k]; !exists {
			permDelta[k] = string(v)
		}
	}
	if len(permDelta) > 0 {
		delta["tool_permissions"] = permDelta
	}
}

func (*Config) diffSubAgents(current, global *SubAgentConfig, delta map[string]interface{}) {
	subDelta := map[string]interface{}{}
	if current.MaxConcurrent != global.MaxConcurrent && current.MaxConcurrent != 0 {
		subDelta["max_concurrent"] = current.MaxConcurrent
	}
	if current.Timeout != global.Timeout && current.Timeout != 0 {
		subDelta["timeout"] = current.Timeout.String()
	}
	if len(subDelta) > 0 {
		delta["subagents"] = subDelta
	}
}

func (*Config) diffSwarm(current, global *SwarmConfig, delta map[string]interface{}) {
	swDelta := map[string]interface{}{}
	if current.MaxTeammatesPerTeam != global.MaxTeammatesPerTeam && current.MaxTeammatesPerTeam != 0 {
		swDelta["max_teammates_per_team"] = current.MaxTeammatesPerTeam
	}
	if current.TeammateTimeout != global.TeammateTimeout && current.TeammateTimeout != 0 {
		swDelta["teammate_timeout"] = current.TeammateTimeout.String()
	}
	if current.InboxSize != global.InboxSize && current.InboxSize != 0 {
		swDelta["inbox_size"] = current.InboxSize
	}
	if current.PollInterval != global.PollInterval && current.PollInterval != 0 {
		swDelta["poll_interval"] = current.PollInterval.String()
	}
	if len(swDelta) > 0 {
		delta["swarm"] = swDelta
	}
}

func (*Config) diffHooks(current, global *hooks.HookConfig, delta map[string]interface{}) {
	hookDelta := map[string]interface{}{}
	if len(current.PreToolUse) > 0 && len(global.PreToolUse) == 0 {
		hookDelta["pre_tool_use"] = current.PreToolUse
	}
	if len(current.PostToolUse) > 0 && len(global.PostToolUse) == 0 {
		hookDelta["post_tool_use"] = current.PostToolUse
	}
	if len(hookDelta) > 0 {
		delta["hooks"] = hookDelta
	}
}
