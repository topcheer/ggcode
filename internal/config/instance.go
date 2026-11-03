package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"gopkg.in/yaml.v3"
)

const (
	instancesDir = "instances"
	hashLen      = 16
)

func InstanceDir(workspace string) string {
	home := HomeDir()
	normalized := normalizeInstanceWorkspacePath(workspace)
	h := sha256.Sum256([]byte(normalized))
	hash := hex.EncodeToString(h[:])[:hashLen]
	return filepath.Join(home, ".ggcode", instancesDir, hash)
}

func normalizeInstanceWorkspacePath(workspace string) string {
	trimmed := strings.TrimSpace(workspace)
	if trimmed == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(trimmed); err == nil {
		return filepath.Clean(resolved)
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(trimmed)
}

func InstanceConfigPath(workspace string) string {
	dir := InstanceDir(workspace)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "ggcode.yaml")
}

func LoadInstanceConfig(workspace string) *Config {
	path := InstanceConfigPath(workspace)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		debug.Log("config", "instance config parse error %s: %v", path, err)
		return nil
	}
	var rawKeys map[string]interface{}
	if err := yaml.Unmarshal(data, &rawKeys); err == nil {
		cfg.explicitKeys = make(map[string]bool, len(rawKeys))
		flattenExplicitKeys("", rawKeys, cfg.explicitKeys)
	}
	cfg.FilePath = path
	return &cfg
}

func flattenExplicitKeys(prefix string, m map[string]interface{}, out map[string]bool) {
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		out[path] = true
		if nested, ok := v.(map[string]interface{}); ok {
			flattenExplicitKeys(path, nested, out)
		}
	}
}

func HasInstanceConfig(workspace string) bool {
	path := InstanceConfigPath(workspace)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func MergeInstance(global, instance *Config) {
	if instance == nil {
		return
	}
	if global.instanceFields == nil {
		global.instanceFields = make(map[string]bool)
	}

	explicit := func(key string) bool { return instance.explicitKeys[key] }
	if (global.Language == "" && instance.Language != "") || (explicit("language") && instance.Language != "") {
		global.Language = instance.Language
		global.instanceFields["language"] = true
	}
	if (global.ExtraPrompt == "" && instance.ExtraPrompt != "") || explicit("system_prompt") {
		global.ExtraPrompt = instance.ExtraPrompt
		global.instanceFields["system_prompt"] = true
	}
	if (global.DefaultMode == "" && instance.DefaultMode != "") || (explicit("default_mode") && instance.DefaultMode != "") {
		global.DefaultMode = instance.DefaultMode
		global.instanceFields["default_mode"] = true
	}
	if (global.MaxIterations == 0 && instance.MaxIterations != 0) || explicit("max_iterations") {
		global.MaxIterations = instance.MaxIterations
		global.instanceFields["max_iterations"] = true
	}

	mergeUIConfig(&global.UI, &instance.UI, global.instanceFields)

	mergeIMConfig(&global.IM, &instance.IM, global.instanceFields)

	if global.Vendors == nil && instance.Vendors != nil {
		global.Vendors = make(map[string]VendorConfig, len(instance.Vendors))
	}

	for k, v := range instance.Vendors {

		if gv, exists := global.Vendors[k]; !exists || !reflect.DeepEqual(gv, v) {
			global.Vendors[k] = v
			global.instanceFields["vendors"] = true
		}
	}

	if len(global.AllowedDirs) == 0 && len(instance.AllowedDirs) > 0 {
		global.AllowedDirs = instance.AllowedDirs
		global.instanceFields["allowed_dirs"] = true
	}

	if global.ToolPerms == nil && len(instance.ToolPerms) > 0 {
		global.ToolPerms = make(map[string]ToolPermission, len(instance.ToolPerms))
	}
	for k, v := range instance.ToolPerms {
		if gv, exists := global.ToolPerms[k]; !exists || gv != v {
			global.ToolPerms[k] = v
			global.instanceFields["tool_permissions"] = true
		}
	}

	if len(global.MCPServers) == 0 && len(instance.MCPServers) > 0 {
		global.MCPServers = instance.MCPServers
		global.instanceFields["mcp_servers"] = true
	}

	if len(global.Plugins) == 0 && len(instance.Plugins) > 0 {
		global.Plugins = instance.Plugins
		global.instanceFields["plugins"] = true
	}

	mergeKnightConfig(&global.KnightConfig, &instance.KnightConfig, global.instanceFields, explicit)

	mergeA2AConfigFields(&global.A2A, &instance.A2A, global.instanceFields, explicit)

	mergeSubAgentConfig(&global.SubAgents, &instance.SubAgents, global.instanceFields)

	instanceImpNonZero := instance.Impersonation.Preset != "" ||
		instance.Impersonation.CustomVersion != "" ||
		len(instance.Impersonation.CustomHeaders) > 0
	if global.Impersonation.Preset == "" && instanceImpNonZero {
		global.Impersonation = instance.Impersonation

		global.instanceFields["impersonation"] = true
	}

	mergeSwarmConfig(&global.Swarm, &instance.Swarm, global.instanceFields)

	mergeHookConfig(&global.Hooks, &instance.Hooks, global.instanceFields)
}

func mergeUIConfig(global, instance *UIConfig, tracked map[string]bool) {
	if global.SidebarVisible == nil && instance.SidebarVisible != nil {
		global.SidebarVisible = instance.SidebarVisible
		tracked["ui"] = true
	}
}

func mergeIMConfig(global, instance *IMConfig, tracked map[string]bool) {
	if !global.Enabled && instance.Enabled {
		global.Enabled = instance.Enabled
		tracked["im"] = true
	}
	if global.ActiveSessionPolicy == "" && instance.ActiveSessionPolicy != "" {
		global.ActiveSessionPolicy = instance.ActiveSessionPolicy
		tracked["im"] = true
	}
	if global.RequireLocalSession == nil && instance.RequireLocalSession != nil {
		global.RequireLocalSession = instance.RequireLocalSession
		tracked["im"] = true
	}
	if global.OutputMode == "" && instance.OutputMode != "" {
		global.OutputMode = instance.OutputMode
		tracked["im"] = true
	}
	if !global.Streaming.Enabled && instance.Streaming.Enabled {
		global.Streaming = instance.Streaming
		tracked["im"] = true
	}
	if global.STT.Provider == "" && instance.STT.Provider != "" {
		global.STT = instance.STT
		tracked["im"] = true
	}

	if global.Adapters == nil && instance.Adapters != nil {
		global.Adapters = make(map[string]IMAdapterConfig, len(instance.Adapters))
		tracked["im"] = true
	}
	for k, v := range instance.Adapters {
		if _, exists := global.Adapters[k]; !exists {
			global.Adapters[k] = v
			tracked["im"] = true
		}
	}
}

func mergeKnightConfig(global, instance *KnightConfig, tracked map[string]bool, explicit func(string) bool) {

	if !global.Enabled && instance.Enabled {

		global.Enabled = instance.Enabled
		tracked["knight"] = true
	} else if explicit("knight.enabled") && global.Enabled != instance.Enabled {
		global.Enabled = instance.Enabled
		tracked["knight"] = true
	}

	if global.TrustLevel == "" && instance.TrustLevel != "" || explicit("knight.trust_level") {
		global.TrustLevel = instance.TrustLevel
		tracked["knight"] = true
	}
	if global.DailyTokenBudget == 0 && instance.DailyTokenBudget != 0 || explicit("knight.daily_token_budget") {
		global.DailyTokenBudget = instance.DailyTokenBudget
		tracked["knight"] = true
	}
	if global.IdleDelaySec == 0 && instance.IdleDelaySec != 0 || explicit("knight.idle_delay_sec") {
		global.IdleDelaySec = instance.IdleDelaySec
		tracked["knight"] = true
	}
	if len(global.Capabilities) == 0 && len(instance.Capabilities) > 0 {
		global.Capabilities = instance.Capabilities
		tracked["knight"] = true
	}
	if global.Vendor == "" && instance.Vendor != "" {
		global.Vendor = instance.Vendor
		tracked["knight"] = true
	}
	if global.Endpoint == "" && instance.Endpoint != "" {
		global.Endpoint = instance.Endpoint
		tracked["knight"] = true
	}
	if global.Model == "" && instance.Model != "" {
		global.Model = instance.Model
		tracked["knight"] = true
	}
}

func mergeA2AConfigFields(global, instance *A2AConfig, tracked map[string]bool, explicit func(string) bool) {

	if !global.Disabled && instance.Disabled || explicit("a2a.disabled") {
		if global.Disabled != instance.Disabled {
			global.Disabled = instance.Disabled
			tracked["a2a"] = true
		}
	}
	if global.Port == 0 && instance.Port != 0 || explicit("a2a.port") {
		global.Port = instance.Port
		tracked["a2a"] = true
	}
	if global.Host == "" && instance.Host != "" || explicit("a2a.host") {
		global.Host = instance.Host
		tracked["a2a"] = true
	}
	if global.MaxTasks == 0 && instance.MaxTasks != 0 || explicit("a2a.max_tasks") {
		global.MaxTasks = instance.MaxTasks
		tracked["a2a"] = true
	}
	if global.TaskTimeout == "" && instance.TaskTimeout != "" || explicit("a2a.task_timeout") {
		global.TaskTimeout = instance.TaskTimeout
		tracked["a2a"] = true
	}
	if global.Auth.APIKey == "" && instance.Auth.APIKey != "" || explicit("a2a.auth.api_key") {
		global.Auth.APIKey = instance.Auth.APIKey
		tracked["a2a"] = true
	}
	if len(global.Auth.APIKeys) == 0 && len(instance.Auth.APIKeys) > 0 {
		global.Auth.APIKeys = instance.Auth.APIKeys
		tracked["a2a"] = true
	}
	if global.Auth.OAuth2 == nil && instance.Auth.OAuth2 != nil {
		global.Auth.OAuth2 = instance.Auth.OAuth2
		tracked["a2a"] = true
	}
	if global.Auth.OIDC == nil && instance.Auth.OIDC != nil {
		global.Auth.OIDC = instance.Auth.OIDC
		tracked["a2a"] = true
	}
	if global.Auth.MTLS == nil && instance.Auth.MTLS != nil {
		global.Auth.MTLS = instance.Auth.MTLS
		tracked["a2a"] = true
	}
}

func mergeSubAgentConfig(global, instance *SubAgentConfig, tracked map[string]bool) {
	if global.MaxConcurrent == 0 && instance.MaxConcurrent != 0 {
		global.MaxConcurrent = instance.MaxConcurrent
		tracked["subagents"] = true
	}
	if global.Timeout == 0 && instance.Timeout != 0 {
		global.Timeout = instance.Timeout
		tracked["subagents"] = true
	}
}

func mergeSwarmConfig(global, instance *SwarmConfig, tracked map[string]bool) {
	if global.MaxTeammatesPerTeam == 0 && instance.MaxTeammatesPerTeam != 0 {
		global.MaxTeammatesPerTeam = instance.MaxTeammatesPerTeam
		tracked["swarm"] = true
	}
	if global.TeammateTimeout == 0 && instance.TeammateTimeout != 0 {
		global.TeammateTimeout = instance.TeammateTimeout
		tracked["swarm"] = true
	}
	if global.InboxSize == 0 && instance.InboxSize != 0 {
		global.InboxSize = instance.InboxSize
		tracked["swarm"] = true
	}
	if global.PollInterval == 0 && instance.PollInterval != 0 {
		global.PollInterval = instance.PollInterval
		tracked["swarm"] = true
	}
}

func mergeHookConfig(global, instance *hooks.HookConfig, tracked map[string]bool) {
	if len(instance.PreToolUse) > 0 {
		global.PreToolUse = instance.PreToolUse
		tracked["hooks"] = true
	}
	if len(instance.PostToolUse) > 0 {
		global.PostToolUse = instance.PostToolUse
		tracked["hooks"] = true
	}
}

func (c *Config) SaveInstance(workspace string) error {
	dir := InstanceDir(workspace)
	if dir == "" {
		return fmt.Errorf("cannot determine instance directory for workspace %s", workspace)
	}
	if err := ensureSecureConfigDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, "ggcode.yaml")

	delta := c.marshalInstanceDelta()

	var existingRaw map[string]interface{}
	existingData, readErr := os.ReadFile(path)
	if readErr == nil && len(existingData) > 0 {
		existingRaw = map[string]interface{}{}
		if yamlErr := yaml.Unmarshal(existingData, &existingRaw); yamlErr != nil {
			existingRaw = nil
		}
	}
	if existingRaw != nil {
		applyInstanceResets(existingRaw, delta, c.instanceFields)
	}

	if len(delta) == 0 && (existingRaw == nil || len(existingRaw) == 0) {

		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				debug.Log("config", "SaveInstance: failed to remove empty instance config: %v", err)
			}
		}
		return nil
	}

	var data []byte
	if existingRaw != nil && len(existingRaw) > 0 {
		deepMergeYAMLMaps(existingRaw, delta)

		if len(existingRaw) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				debug.Log("config", "SaveInstance: failed to remove fully-reset instance config: %v", err)
			}
			return nil
		}
		var marshalErr error
		data, marshalErr = yaml.Marshal(existingRaw)
		if marshalErr != nil {
			return fmt.Errorf("marshaling merged instance config: %w", marshalErr)
		}
	} else {
		var marshalErr error
		data, marshalErr = yaml.Marshal(delta)
		if marshalErr != nil {
			return fmt.Errorf("marshaling instance config: %w", marshalErr)
		}
	}

	if err := writeFileAtomic(path, data, secureConfigFileMode); err != nil {
		return err
	}

	if c.instanceDir != "" {
		hash := filepath.Base(c.instanceDir)
		if migrated, migrateErr := MigrateInstancePlaintextAPIKeys(path, hash); migrateErr != nil {
			debug.Log("config", "SaveInstance: migration error: %v", migrateErr)
		} else if len(migrated) > 0 {
		}
	}

	return nil
}

func (c *Config) SetInstancePaths(workspace string) {
	dir := InstanceDir(workspace)
	if dir != "" {
		c.instanceDir = dir
		c.instancePath = filepath.Join(dir, "ggcode.yaml")
		c.instanceWS = workspace
	}
}

func (c *Config) InstanceDirPath() string {
	return c.instanceDir
}

func (c *Config) HasInstanceConfigAttached() bool {
	return c.instanceWS != ""
}

func (c *Config) HasInstanceConfigFile() bool {
	if c.instancePath == "" {
		return false
	}
	_, err := os.Stat(c.instancePath)
	return err == nil
}

func (c *Config) InstanceWorkspace() string {
	return c.instanceWS
}

func (c *Config) SyncVendorToGlobalSnapshot(vendor string) {
	if c == nil || vendor == "" {
		return
	}
	if c.globalSnap == nil {
		return
	}
	if c.globalSnap.Vendors == nil {
		c.globalSnap.Vendors = make(map[string]VendorConfig)
	}
	if vc, ok := c.Vendors[vendor]; ok {
		c.globalSnap.Vendors[vendor] = vc
	}
}

func (c *Config) VendorNotInGlobalSnapshot(vendor string) bool {
	if c == nil || c.globalSnap == nil || vendor == "" {
		return false
	}
	if _, inSnap := c.globalSnap.Vendors[vendor]; inSnap {
		return false
	}
	_, inCur := c.Vendors[vendor]
	return inCur
}

func (c *Config) SaveScoped(scope string) error {
	c.saveScope = scope
	switch scope {
	case "instance":
		return c.SaveInstance(c.instanceWS)
	default:
		return c.Save()
	}
}

func (c *Config) SaveInstanceScoped(workspace string) error {
	return c.SaveInstance(workspace)
}

func (c *Config) InstanceFields() []string {
	if c == nil || len(c.instanceFields) == 0 {
		return nil
	}
	keys := make([]string, 0, len(c.instanceFields))
	for k := range c.instanceFields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *Config) SetSaveScope(scope string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case "", "global":
		c.saveScope = "global"
		return nil
	case "instance":
		if strings.TrimSpace(c.instanceWS) == "" {
			return fmt.Errorf("instance config is not attached")
		}
		c.saveScope = "instance"
		return nil
	default:
		return fmt.Errorf("unknown save scope %q", scope)
	}
}

func writeFileAtomic(path string, data []byte, _ os.FileMode) error {
	return writeSecureConfigFile(path, data)
}

func LoadWithInstance(path, workspace string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}

	cfg.globalSnap = deepCopyConfig(cfg)

	MigrateA2AYaml(workspace)

	instDir := InstanceDir(workspace)
	LoadInstanceKeysEnv(instDir)

	instanceCfg := LoadInstanceConfig(workspace)
	if instanceCfg != nil {
		MergeInstance(cfg, instanceCfg)
	}

	cfg.SetInstancePaths(workspace)

	if a2aOverride := LoadA2AOverride(workspace); a2aOverride != nil {
		MergeA2AConfig(&cfg.A2A, a2aOverride)

		if cfg.instanceFields == nil {
			cfg.instanceFields = make(map[string]bool)
		}
		cfg.instanceFields["a2a"] = true
	}

	return cfg, nil
}

func MigrateA2AYaml(workspace string) bool {
	legacyPath := filepath.Join(workspace, ".ggcode", "a2a.yaml")
	if _, err := os.Stat(legacyPath); err != nil {
		return false
	}

	if HasInstanceConfig(workspace) {
		return false
	}

	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return false
	}

	var a2aRaw map[string]interface{}
	if err := yaml.Unmarshal(data, &a2aRaw); err != nil {
		return false
	}

	instanceData := map[string]interface{}{
		"a2a": a2aRaw,
	}
	out, err := yaml.Marshal(instanceData)
	if err != nil {
		return false
	}

	instDir := InstanceDir(workspace)
	if instDir == "" {
		return false
	}
	if err := ensureSecureConfigDir(instDir); err != nil {
		return false
	}
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := writeFileAtomic(instPath, out, secureConfigFileMode); err != nil {
		return false
	}

	if err := os.Remove(legacyPath); err != nil {
		debug.Log("config", "a2a migration: legacy file left in place (remove failed, values may re-merge): %v", err)
	}

	return true
}

func (c *Config) InstanceSummary() string {
	if c.instanceDir == "" {
		return "no instance config"
	}
	return fmt.Sprintf("instance dir: %s", c.instanceDir)
}

func deepCopyConfig(src *Config) *Config {
	if src == nil {
		return nil
	}
	data, err := yaml.Marshal(src)
	if err != nil {
		debug.Log("config", "deepCopyConfig marshal error: %v", err)
		return nil
	}
	var dst Config
	if err := yaml.Unmarshal(data, &dst); err != nil {
		debug.Log("config", "deepCopyConfig unmarshal error: %v", err)
		return nil
	}
	return &dst
}
