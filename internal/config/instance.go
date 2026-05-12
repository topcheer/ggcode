package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"gopkg.in/yaml.v3"
)

const (
	// instancesDir is the subdirectory under ~/.ggcode/ for instance configs.
	instancesDir = "instances"
	// hashLen is the number of hex chars used from the SHA256 hash.
	hashLen = 16
)

// InstanceDir returns the per-workspace instance config directory path.
// Format: ~/.ggcode/instances/{sha256(abs-workspace)[:16]}
// Returns empty string if the home directory cannot be resolved.
func InstanceDir(workspace string) string {
	home := HomeDir()
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	h := sha256.Sum256([]byte(abs))
	hash := hex.EncodeToString(h[:])[:hashLen]
	return filepath.Join(home, ".ggcode", instancesDir, hash)
}

// InstanceConfigPath returns the full path to the instance config file.
// Returns empty string if InstanceDir fails.
func InstanceConfigPath(workspace string) string {
	dir := InstanceDir(workspace)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "ggcode.yaml")
}

// LoadInstanceConfig loads the instance-level config for a workspace.
// Returns nil if no instance config file exists.
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
	cfg.FilePath = path
	debug.Log("config", "loaded instance config from %s", path)
	return &cfg
}

// HasInstanceConfig returns true if an instance config file exists for the workspace.
func HasInstanceConfig(workspace string) bool {
	path := InstanceConfigPath(workspace)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// MergeInstance applies instance config on top of global (base) config.
// Rule: global non-zero fields are never overwritten by instance values.
// Instance values only fill in gaps where global fields are zero/empty.
// Fields filled by instance are recorded in instanceFields to prevent
// them from leaking back into the global file on Save().
func MergeInstance(global, instance *Config) {
	if instance == nil {
		return
	}
	if global.instanceFields == nil {
		global.instanceFields = make(map[string]bool)
	}

	// Simple scalar fields — only override if global is zero.
	if global.Vendor == "" && instance.Vendor != "" {
		global.Vendor = instance.Vendor
		global.instanceFields["vendor"] = true
	}
	if global.Endpoint == "" && instance.Endpoint != "" {
		global.Endpoint = instance.Endpoint
		global.instanceFields["endpoint"] = true
	}
	if global.Model == "" && instance.Model != "" {
		global.Model = instance.Model
		global.instanceFields["model"] = true
	}
	if global.Language == "" && instance.Language != "" {
		global.Language = instance.Language
		global.instanceFields["language"] = true
	}
	if global.ExtraPrompt == "" && instance.ExtraPrompt != "" {
		global.ExtraPrompt = instance.ExtraPrompt
		global.instanceFields["system_prompt"] = true
	}
	if global.DefaultMode == "" && instance.DefaultMode != "" {
		global.DefaultMode = instance.DefaultMode
		global.instanceFields["default_mode"] = true
	}
	if global.MaxIterations == 0 && instance.MaxIterations != 0 {
		global.MaxIterations = instance.MaxIterations
		global.instanceFields["max_iterations"] = true
	}

	// UI
	mergeUIConfig(&global.UI, &instance.UI, global.instanceFields)

	// IM
	mergeIMConfig(&global.IM, &instance.IM, global.instanceFields)

	// Vendors — add instance vendors not in global
	if global.Vendors == nil && instance.Vendors != nil {
		global.Vendors = make(map[string]VendorConfig, len(instance.Vendors))
		global.instanceFields["vendors"] = true
	}
	for k, v := range instance.Vendors {
		if _, exists := global.Vendors[k]; !exists {
			global.Vendors[k] = v
		}
	}

	// AllowedDirs — only use instance if global is empty
	if len(global.AllowedDirs) == 0 && len(instance.AllowedDirs) > 0 {
		global.AllowedDirs = instance.AllowedDirs
		global.instanceFields["allowed_dirs"] = true
	}

	// ToolPerms — add instance permissions not in global
	if global.ToolPerms == nil && instance.ToolPerms != nil {
		global.ToolPerms = make(map[string]ToolPermission, len(instance.ToolPerms))
		global.instanceFields["tool_permissions"] = true
	}
	for k, v := range instance.ToolPerms {
		if _, exists := global.ToolPerms[k]; !exists {
			global.ToolPerms[k] = v
		}
	}

	// MCPServers — only use instance if global is empty
	if len(global.MCPServers) == 0 && len(instance.MCPServers) > 0 {
		global.MCPServers = instance.MCPServers
		global.instanceFields["mcp_servers"] = true
	}

	// Plugins — only use instance if global is empty
	if len(global.Plugins) == 0 && len(instance.Plugins) > 0 {
		global.Plugins = instance.Plugins
		global.instanceFields["plugins"] = true
	}

	// Knight
	mergeKnightConfig(&global.KnightConfig, &instance.KnightConfig)

	// A2A
	mergeA2AConfigFields(&global.A2A, &instance.A2A)

	// SubAgents
	mergeSubAgentConfig(&global.SubAgents, &instance.SubAgents)

	// Impersonation
	if global.Impersonation.Preset == "" && instance.Impersonation.Preset != "" {
		global.Impersonation = instance.Impersonation
	}

	// Swarm
	mergeSwarmConfig(&global.Swarm, &instance.Swarm)

	// Hooks
	mergeHookConfig(&global.Hooks, &instance.Hooks)
}

// mergeUIConfig merges instance UI config into global.
func mergeUIConfig(global, instance *UIConfig, tracked map[string]bool) {
	if global.SidebarVisible == nil && instance.SidebarVisible != nil {
		global.SidebarVisible = instance.SidebarVisible
		tracked["ui"] = true
	}
}

// mergeIMConfig merges instance IM config into global.
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
	// Adapters — add instance adapters not in global
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

// mergeKnightConfig merges instance Knight config into global.
func mergeKnightConfig(global, instance *KnightConfig) {
	// Knight.Enabled default is true; only override if global is explicitly false
	// and instance is true, or vice versa. Since we can't distinguish "explicitly
	// false" from "default false" without the YAML raw data, we use the simple rule:
	// instance wins only if global is at default.
	// For simplicity: if instance sets Enabled differently, we respect instance
	// only when global is at the DefaultKnightConfig value.
	if !global.Enabled && instance.Enabled {
		// Global is false (could be default or explicit), instance wants true.
		// Since we can't tell, we let instance fill the gap.
		// NOTE: This means if user explicitly set enabled:false in global,
		// instance CAN turn it back on. This is by design — the instance admin
		// may want to enable knight for a specific project.
		global.Enabled = instance.Enabled
	}

	if global.TrustLevel == "" && instance.TrustLevel != "" {
		global.TrustLevel = instance.TrustLevel
	}
	if global.DailyTokenBudget == 0 && instance.DailyTokenBudget != 0 {
		global.DailyTokenBudget = instance.DailyTokenBudget
	}
	if global.IdleDelaySec == 0 && instance.IdleDelaySec != 0 {
		global.IdleDelaySec = instance.IdleDelaySec
	}
	if len(global.Capabilities) == 0 && len(instance.Capabilities) > 0 {
		global.Capabilities = instance.Capabilities
	}
	if global.Vendor == "" && instance.Vendor != "" {
		global.Vendor = instance.Vendor
	}
	if global.Endpoint == "" && instance.Endpoint != "" {
		global.Endpoint = instance.Endpoint
	}
	if global.Model == "" && instance.Model != "" {
		global.Model = instance.Model
	}
}

// mergeA2AConfigFields merges instance A2A config into global.
// Uses the same "global wins if set" rule.
func mergeA2AConfigFields(global, instance *A2AConfig) {
	if !global.Disabled && instance.Disabled {
		global.Disabled = instance.Disabled
	}
	if global.Port == 0 && instance.Port != 0 {
		global.Port = instance.Port
	}
	if global.Host == "" && instance.Host != "" {
		global.Host = instance.Host
	}
	if global.APIKey == "" && instance.APIKey != "" {
		global.APIKey = instance.APIKey
	}
	if global.MaxTasks == 0 && instance.MaxTasks != 0 {
		global.MaxTasks = instance.MaxTasks
	}
	if global.TaskTimeout == "" && instance.TaskTimeout != "" {
		global.TaskTimeout = instance.TaskTimeout
	}
	if !global.LANDiscovery && instance.LANDiscovery {
		global.LANDiscovery = instance.LANDiscovery
	}
	if global.Auth.APIKey == "" && instance.Auth.APIKey != "" {
		global.Auth.APIKey = instance.Auth.APIKey
	}
	if len(global.Auth.APIKeys) == 0 && len(instance.Auth.APIKeys) > 0 {
		global.Auth.APIKeys = instance.Auth.APIKeys
	}
	if global.Auth.OAuth2 == nil && instance.Auth.OAuth2 != nil {
		global.Auth.OAuth2 = instance.Auth.OAuth2
	}
	if global.Auth.OIDC == nil && instance.Auth.OIDC != nil {
		global.Auth.OIDC = instance.Auth.OIDC
	}
	if global.Auth.MTLS == nil && instance.Auth.MTLS != nil {
		global.Auth.MTLS = instance.Auth.MTLS
	}
}

// mergeSubAgentConfig merges instance subagent config into global.
func mergeSubAgentConfig(global, instance *SubAgentConfig) {
	if global.MaxConcurrent == 0 && instance.MaxConcurrent != 0 {
		global.MaxConcurrent = instance.MaxConcurrent
	}
	if global.Timeout == 0 && instance.Timeout != 0 {
		global.Timeout = instance.Timeout
	}
}

// mergeSwarmConfig merges instance swarm config into global.
func mergeSwarmConfig(global, instance *SwarmConfig) {
	if global.MaxTeammatesPerTeam == 0 && instance.MaxTeammatesPerTeam != 0 {
		global.MaxTeammatesPerTeam = instance.MaxTeammatesPerTeam
	}
	if global.TeammateTimeout == 0 && instance.TeammateTimeout != 0 {
		global.TeammateTimeout = instance.TeammateTimeout
	}
	if global.InboxSize == 0 && instance.InboxSize != 0 {
		global.InboxSize = instance.InboxSize
	}
	if global.PollInterval == 0 && instance.PollInterval != 0 {
		global.PollInterval = instance.PollInterval
	}
}

// mergeHookConfig merges instance hook config into global.
func mergeHookConfig(global, instance *hooks.HookConfig) {
	if len(global.PreToolUse) == 0 && len(instance.PreToolUse) > 0 {
		global.PreToolUse = instance.PreToolUse
	}
	if len(global.PostToolUse) == 0 && len(instance.PostToolUse) > 0 {
		global.PostToolUse = instance.PostToolUse
	}
}

// SaveInstance saves only the fields that differ from the global config
// into the instance-level override file at ~/.ggcode/instances/{sha256}/ggcode.yaml.
//
// Strategy: read the existing instance file (if any), compute the diff between
// the current Config and the globalSnap (the original global values), and write
// only the changed fields. If no fields differ, the instance file is deleted.
//
// This ensures the instance file remains minimal — only the override values,
// never a full copy of the config (which would bloat disk usage and leak
// system_prompt / vendor definitions / secrets into per-workspace files).
func (c *Config) SaveInstance(workspace string) error {
	dir := InstanceDir(workspace)
	if dir == "" {
		return fmt.Errorf("cannot determine instance directory for workspace %s", workspace)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating instance config directory: %w", err)
	}
	path := filepath.Join(dir, "ggcode.yaml")

	// Compute the minimal diff: fields in current config that differ from globalSnap.
	delta := c.marshalInstanceDelta()

	if len(delta) == 0 {
		// No overrides — remove the instance file if it exists.
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				debug.Log("config", "SaveInstance: failed to remove empty instance config: %v", err)
			} else {
				debug.Log("config", "SaveInstance: removed empty instance config %s", path)
			}
		}
		return nil
	}

	data, err := yaml.Marshal(delta)
	if err != nil {
		return fmt.Errorf("marshaling instance config: %w", err)
	}
	if err := writeFileAtomic(path, data, 0644); err != nil {
		return err
	}
	debug.Log("config", "saved instance config to %s (%d bytes)", path, len(data))

	// Migrate any plaintext API keys in the instance config to instance keys.env.
	// Use instance-prefixed env vars to avoid overwriting global keys.
	if c.instanceDir != "" {
		hash := filepath.Base(c.instanceDir) // the SHA256 short hash
		if migrated, migrateErr := MigrateInstancePlaintextAPIKeys(path, hash); migrateErr != nil {
			debug.Log("config", "SaveInstance: migration error: %v", migrateErr)
		} else if len(migrated) > 0 {
			debug.Log("config", "SaveInstance: migrated %d plaintext secret(s)", len(migrated))
		}
	}

	return nil
}

// SetInstancePaths records the instance config directory and path on the Config.
// This is called after MergeInstance so that Save() can know about the instance context.
func (c *Config) SetInstancePaths(workspace string) {
	dir := InstanceDir(workspace)
	if dir != "" {
		c.instanceDir = dir
		c.instancePath = filepath.Join(dir, "ggcode.yaml")
		c.instanceWS = workspace
	}
}

// InstanceDirPath returns the instance directory for this config, or empty string.
func (c *Config) InstanceDirPath() string {
	return c.instanceDir
}

// HasInstanceConfigAttached returns true if this config has an instance workspace attached.
func (c *Config) HasInstanceConfigAttached() bool {
	return c.instanceWS != ""
}

// HasInstanceConfigFile returns true if an instance config file actually exists on disk.
func (c *Config) HasInstanceConfigFile() bool {
	if c.instancePath == "" {
		return false
	}
	_, err := os.Stat(c.instancePath)
	return err == nil
}

// InstanceWorkspace returns the workspace path for instance config, or empty string.
func (c *Config) InstanceWorkspace() string {
	return c.instanceWS
}

// SaveScoped persists config changes to either global or instance config.
// scope: "global" saves to the global config file (default behavior).
// scope: "instance" saves to the instance config directory.
// Also sets c.saveScope so that subsequent Save*Preference calls use the same target.
func (c *Config) SaveScoped(scope string) error {
	c.saveScope = scope
	switch scope {
	case "instance":
		return c.SaveInstance(c.instanceWS)
	default:
		return c.Save()
	}
}

// writeFileAtomic writes data to a temp file then renames.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ggcode-instance-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

// LoadWithInstance loads the global config and applies instance-level overrides.
// workspace is the current working directory used to locate the instance config.
// It saves a snapshot of the global config so that Save() can avoid leaking
// instance-level fields back into the global file.
func LoadWithInstance(path, workspace string) (*Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}

	// Save a deep copy of the global config before any instance merge.
	// This allows Save() to distinguish global fields from instance overrides.
	cfg.globalSnap = deepCopyConfig(cfg)

	// Attempt to migrate legacy .ggcode/a2a.yaml if instance config doesn't exist yet.
	MigrateA2AYaml(workspace)

	// Load instance-level API keys into the environment so ${VAR} expansion
	// in the instance config can resolve them. Instance keys override globals.
	instDir := InstanceDir(workspace)
	LoadInstanceKeysEnv(instDir)

	// Apply instance-level config if available.
	instanceCfg := LoadInstanceConfig(workspace)
	if instanceCfg != nil {
		MergeInstance(cfg, instanceCfg)
		debug.Log("config", "applied instance config for workspace %s", workspace)
	}
	// Always record instance paths so HasInstanceConfigAttached() returns true
	// even when no instance config file exists yet. This allows the user to
	// switch to instance scope and create the first instance config.
	cfg.SetInstancePaths(workspace)

	// Also apply legacy .ggcode/a2a.yaml if it exists (for backward compat).
	// This uses "instance wins" semantics (unlike MergeInstance's "global wins").
	if a2aOverride := LoadA2AOverride(workspace); a2aOverride != nil {
		MergeA2AConfig(&cfg.A2A, a2aOverride)
		debug.Log("config", "applied legacy .ggcode/a2a.yaml override")
	}

	return cfg, nil
}

// MigrateA2AYaml checks if a legacy .ggcode/a2a.yaml exists and offers
// to migrate it to the new instance config system.
// Returns true if migration was performed or already done.
func MigrateA2AYaml(workspace string) bool {
	legacyPath := filepath.Join(workspace, ".ggcode", "a2a.yaml")
	if _, err := os.Stat(legacyPath); err != nil {
		return false // no legacy file
	}

	// Legacy file exists. Check if instance config already exists.
	if HasInstanceConfig(workspace) {
		return false // already migrated or manually created
	}

	// Read the legacy A2A config
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return false
	}

	// Parse as raw map to wrap it under "a2a:" key
	var a2aRaw map[string]interface{}
	if err := yaml.Unmarshal(data, &a2aRaw); err != nil {
		return false
	}

	// Create instance config with A2A content
	instanceData := map[string]interface{}{
		"a2a": a2aRaw,
	}
	out, err := yaml.Marshal(instanceData)
	if err != nil {
		return false
	}

	// Write to instance config
	instDir := InstanceDir(workspace)
	if instDir == "" {
		return false
	}
	if err := os.MkdirAll(instDir, 0755); err != nil {
		return false
	}
	instPath := filepath.Join(instDir, "ggcode.yaml")
	if err := writeFileAtomic(instPath, out, 0644); err != nil {
		return false
	}

	debug.Log("config", "migrated legacy .ggcode/a2a.yaml to %s", instPath)
	return true
}

// InstanceSummary returns a debug string about the instance config state.
func (c *Config) InstanceSummary() string {
	if c.instanceDir == "" {
		return "no instance config"
	}
	return fmt.Sprintf("instance dir: %s", c.instanceDir)
}

// deepCopyConfig creates a deep copy of a Config via YAML round-trip.
// Fields tagged yaml:"-" are lost; this is intentional because we only
// need the serializable fields for comparison.
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
