package config

import (
	"reflect"

	"github.com/topcheer/ggcode/internal/hooks"
	"gopkg.in/yaml.v3"
)

func (c *Config) marshalInstanceDelta() map[string]interface{} {
	delta := map[string]interface{}{}

	if c.globalSnap == nil {

		if c.FilePath != "" {
			if globalCfg, err := Load(c.FilePath); err == nil {
				c.globalSnap = globalCfg
			}
		}
		if c.globalSnap == nil {

			c.globalSnap = &Config{}
		}
	}

	c.diffScalar("vendor", c.Vendor, c.globalSnap.Vendor, delta)
	c.diffScalar("endpoint", c.Endpoint, c.globalSnap.Endpoint, delta)
	c.diffScalar("model", c.Model, c.globalSnap.Model, delta)
	c.diffScalar("language", c.Language, c.globalSnap.Language, delta)
	c.diffScalar("system_prompt", c.ExtraPrompt, c.globalSnap.ExtraPrompt, delta)
	c.diffScalar("default_mode", c.DefaultMode, c.globalSnap.DefaultMode, delta)
	c.diffInt("max_iterations", c.MaxIterations, c.globalSnap.MaxIterations, delta)

	c.diffUI(&c.UI, &c.globalSnap.UI, delta)

	c.diffIM(&c.IM, &c.globalSnap.IM, delta)

	c.diffVendors(c.Vendors, c.globalSnap.Vendors, delta)

	c.diffA2A(&c.A2A, &c.globalSnap.A2A, delta)

	c.diffKnight(&c.KnightConfig, &c.globalSnap.KnightConfig, delta)

	if c.Impersonation.Preset != c.globalSnap.Impersonation.Preset ||
		c.Impersonation.CustomVersion != c.globalSnap.Impersonation.CustomVersion ||
		!reflect.DeepEqual(c.Impersonation.CustomHeaders, c.globalSnap.Impersonation.CustomHeaders) {
		impMap := map[string]interface{}{}
		impMap["preset"] = c.Impersonation.Preset
		impMap["custom_version"] = c.Impersonation.CustomVersion
		impMap["custom_headers"] = c.Impersonation.CustomHeaders
		delta["impersonation"] = impMap
	}

	c.diffStringSlice("allowed_dirs", c.AllowedDirs, c.globalSnap.AllowedDirs, delta)

	c.diffToolPerms(c.ToolPerms, c.globalSnap.ToolPerms, delta)

	c.diffSlice("mcp_servers", c.MCPServers, c.globalSnap.MCPServers, delta)

	c.diffSlice("plugins", c.Plugins, c.globalSnap.Plugins, delta)

	c.diffSubAgents(&c.SubAgents, &c.globalSnap.SubAgents, delta)

	c.diffSwarm(&c.Swarm, &c.globalSnap.Swarm, delta)

	c.diffHooks(&c.Hooks, &c.globalSnap.Hooks, delta)

	return delta
}

func (*Config) diffScalar(key, current, global string, delta map[string]interface{}) {

	if current != global {
		delta[key] = current
	}
}

func (*Config) diffInt(key string, current, global int, delta map[string]interface{}) {

	if current != global {
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

	cData, _ := yaml.Marshal(current)
	gData, _ := yaml.Marshal(global)
	if len(cData) > 0 && string(cData) != string(gData) {
		delta[key] = current
	}
}

func (*Config) diffUI(current, global *UIConfig, delta map[string]interface{}) {
	uiDelta := map[string]interface{}{}

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

	if current.RequireLocalSession != nil && (global.RequireLocalSession == nil ||
		*current.RequireLocalSession != *global.RequireLocalSession) {
		imDelta["require_local_session"] = *current.RequireLocalSession
	}
	if current.OutputMode != global.OutputMode && current.OutputMode != "" {
		imDelta["output_mode"] = current.OutputMode
	}

	if current.Streaming.Enabled != global.Streaming.Enabled {
		streamDelta := map[string]interface{}{}
		streamDelta["enabled"] = current.Streaming.Enabled
		imDelta["streaming"] = streamDelta
	}

	if current.STT.Provider != global.STT.Provider && current.STT.Provider != "" {
		sttDelta := map[string]interface{}{}
		sttDelta["provider"] = current.STT.Provider
		imDelta["stt"] = sttDelta
	}

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

			vendorDelta[k] = v
			continue
		}

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
	if current.Port != global.Port {
		a2aDelta["port"] = current.Port
	}
	if current.Host != global.Host {
		a2aDelta["host"] = current.Host
	}

	if current.MaxTasks != global.MaxTasks {
		a2aDelta["max_tasks"] = current.MaxTasks
	}
	if current.TaskTimeout != global.TaskTimeout {
		a2aDelta["task_timeout"] = current.TaskTimeout
	}

	authDelta := map[string]interface{}{}

	if current.Auth.APIKey != global.Auth.APIKey {
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

	if current.TrustLevel != global.TrustLevel {
		knightDelta["trust_level"] = current.TrustLevel
	}
	if current.DailyTokenBudget != global.DailyTokenBudget {
		knightDelta["daily_token_budget"] = current.DailyTokenBudget
	}
	if current.IdleDelaySec != global.IdleDelaySec {
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

		if gv, exists := global[k]; !exists || gv != v {
			permDelta[k] = string(v)
		}
	}
	if len(permDelta) > 0 {
		delta["tool_permissions"] = permDelta
	}
}

func (*Config) diffSubAgents(current, global *SubAgentConfig, delta map[string]interface{}) {
	subDelta := map[string]interface{}{}

	if current.MaxConcurrent != global.MaxConcurrent {
		subDelta["max_concurrent"] = current.MaxConcurrent
	}
	if current.Timeout != global.Timeout {
		subDelta["timeout"] = current.Timeout.String()
	}
	if len(subDelta) > 0 {
		delta["subagents"] = subDelta
	}
}

func (*Config) diffSwarm(current, global *SwarmConfig, delta map[string]interface{}) {
	swDelta := map[string]interface{}{}

	if current.MaxTeammatesPerTeam != global.MaxTeammatesPerTeam {
		swDelta["max_teammates_per_team"] = current.MaxTeammatesPerTeam
	}
	if current.TeammateTimeout != global.TeammateTimeout {
		swDelta["teammate_timeout"] = current.TeammateTimeout.String()
	}
	if current.InboxSize != global.InboxSize {
		swDelta["inbox_size"] = current.InboxSize
	}
	if current.PollInterval != global.PollInterval {
		swDelta["poll_interval"] = current.PollInterval.String()
	}
	if len(swDelta) > 0 {
		delta["swarm"] = swDelta
	}
}

func (*Config) diffHooks(current, global *hooks.HookConfig, delta map[string]interface{}) {
	hookDelta := map[string]interface{}{}

	if len(current.PreToolUse) > 0 && !hookSlicesEqual(current.PreToolUse, global.PreToolUse) {
		hookDelta["pre_tool_use"] = current.PreToolUse
	}
	if len(current.PostToolUse) > 0 && !hookSlicesEqual(current.PostToolUse, global.PostToolUse) {
		hookDelta["post_tool_use"] = current.PostToolUse
	}
	if len(hookDelta) > 0 {
		delta["hooks"] = hookDelta
	}
}

func hookSlicesEqual(a, b []hooks.Hook) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}
