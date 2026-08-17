// Package wailskit provides a public facade for the Wails desktop app
// to access internal config and other services without violating Go's
// internal package rules.
package wailskit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/hooks"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/stream"
)

var (
	globalCfg *config.Config
	globalMu  sync.RWMutex
)

// SetConfig sets the global config (called after workspace init).
func SetConfig(cfg *config.Config) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalCfg = cfg
}

// GetGlobalConfig returns the current global config.
func GetGlobalConfig() *config.Config {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalCfg
}

// ResolveConfigFilePath finds the config file for a workspace directory.
func ResolveConfigFilePath(workDir string) string {
	for _, p := range []string{
		filepath.Join(workDir, "ggcode.yaml"),
		filepath.Join(workDir, ".ggcode", "ggcode.yaml"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return config.ConfigPath()
}

// LoadConfigForWorkspace loads config for the given workspace.
func LoadConfigForWorkspace(workDir string) (*config.Config, error) {
	cfgPath := ResolveConfigFilePath(workDir)
	return config.LoadWithInstance(cfgPath, workDir)
}

// NeedsOnboard returns true if the config needs first-time setup.
func NeedsOnboard() bool {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return true
	}
	return cfg.NeedsOnboard()
}

// ─── Full Config Snapshot for Frontend ─────────────────────

// FullConfig is a complete read-only snapshot for the frontend settings page.
type FullConfig struct {
	// LLM
	Vendor      string `json:"vendor"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	APIKeySet   bool   `json:"apiKeySet"` // true if key exists (never send key to frontend)
	Language    string `json:"language"`
	ExtraPrompt string `json:"extraPrompt"`
	DefaultMode string `json:"defaultMode"` // auto, allow, confirm
	MaxIter     int    `json:"maxIterations"`
	ProbeCtx    bool   `json:"probeContext"`

	// Impersonation
	ImpersonatePreset        string            `json:"impersonatePreset"`
	ImpersonateCustomVersion string            `json:"impersonateCustomVersion"`
	ImpersonateCustomHeaders map[string]string `json:"impersonateCustomHeaders"`

	// SubAgents
	SubAgentMaxConcurrent int    `json:"subAgentMaxConcurrent"`
	SubAgentTimeout       string `json:"subAgentTimeout"`
	SubAgentShowOutput    bool   `json:"subAgentShowOutput"`

	// Swarm
	SwarmMaxTeammates int    `json:"swarmMaxTeammates"`
	SwarmTimeout      string `json:"swarmTimeout"`
	SwarmInboxSize    int    `json:"swarmInboxSize"`

	// A2A
	A2ADisabled bool   `json:"a2aDisabled"`
	A2APort     int    `json:"a2aPort"`
	A2AHost     string `json:"a2aHost"`

	// Stream (video capture)
	StreamEncoder string `json:"streamEncoder"`
	StreamFPS     int    `json:"streamFPS"`

	// Knight
	KnightEnabled    bool   `json:"knightEnabled"`
	KnightTrustLevel string `json:"knightTrustLevel"`

	// UI
	SidebarVisible *bool `json:"sidebarVisible"`

	// Workspace
	WorkDir string `json:"workDir"`

	// State
	NeedsSetup bool `json:"needsSetup"`
}

// GetFullConfig returns a complete config snapshot.
func GetFullConfig() (*FullConfig, error) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	cfg := globalCfg

	if cfg == nil {
		return &FullConfig{NeedsSetup: true}, nil
	}

	// Check if API key is set (without exposing it)
	// Guard on vendor entry existing; key resolution itself goes through
	// resolveAPIKey so ${VAR} env references are expanded (#584 C3).
	// ExpandEnv intentionally KEEPS unresolved ${VAR} patterns (later stages
	// surface onboarding errors), so an unresolved reference must not be
	// reported as "key is set" here.
	apiKeySet := false
	if _, ok := cfg.Vendors[cfg.Vendor]; ok {
		key := resolveAPIKey(cfg, cfg.Vendor, cfg.Endpoint)
		apiKeySet = key != "" && !strings.Contains(key, "${")
	}

	resolved, _ := cfg.ResolveActiveEndpoint()
	contextWindow := 0
	if resolved != nil {
		contextWindow = resolved.ContextWindow
	}
	_ = contextWindow
	workDir, _ := os.Getwd()

	return &FullConfig{
		Vendor:      cfg.Vendor,
		Endpoint:    cfg.Endpoint,
		Model:       cfg.Model,
		APIKeySet:   apiKeySet,
		Language:    cfg.Language,
		ExtraPrompt: cfg.ExtraPrompt,
		DefaultMode: cfg.DefaultMode,
		MaxIter:     cfg.MaxIterations,
		ProbeCtx:    cfg.ProbeContext,

		ImpersonatePreset:        cfg.Impersonation.Preset,
		ImpersonateCustomVersion: cfg.Impersonation.CustomVersion,
		ImpersonateCustomHeaders: cfg.Impersonation.CustomHeaders,

		SubAgentMaxConcurrent: cfg.SubAgents.MaxConcurrent,
		SubAgentTimeout:       cfg.SubAgents.Timeout.String(),
		SubAgentShowOutput:    cfg.SubAgents.ShowOutput,

		SwarmMaxTeammates: cfg.Swarm.MaxTeammatesPerTeam,
		SwarmTimeout:      cfg.Swarm.TeammateTimeout.String(),
		SwarmInboxSize:    cfg.Swarm.InboxSize,

		A2ADisabled: cfg.A2A.Disabled,
		A2APort:     cfg.A2A.Port,
		A2AHost:     cfg.A2A.Host,

		StreamEncoder: cfg.Stream.HardwareEncoder,
		StreamFPS:     cfg.Stream.FPS,

		KnightEnabled:    cfg.KnightConfig.Enabled,
		KnightTrustLevel: cfg.KnightConfig.TrustLevel,

		SidebarVisible: cfg.UI.SidebarVisible,
		WorkDir:        workDir,
		NeedsSetup:     cfg.NeedsOnboard(),
	}, nil
}

// ─── Config Update Methods ────────────────────────────────

// UpdateConfig applies a map of config values and saves.
func UpdateConfig(values map[string]interface{}) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}

	if v, ok := values["vendor"].(string); ok && v != "" {
		cfg.Vendor = v
	}
	if v, ok := values["endpoint"].(string); ok && v != "" {
		cfg.Endpoint = v
	}
	if v, ok := values["model"].(string); ok && v != "" {
		cfg.Model = v
	}
	if v, ok := values["language"].(string); ok {
		cfg.Language = v
	}
	if v, ok := values["extraPrompt"].(string); ok {
		cfg.ExtraPrompt = v
	}
	if v, ok := values["defaultMode"].(string); ok {
		cfg.DefaultMode = v
	}
	if v, ok := values["baseURL"].(string); ok && v != "" {
		// Update the current endpoint's base_url
		if cfg.Vendor != "" && cfg.Endpoint != "" {
			if cfg.Vendors == nil {
				cfg.Vendors = make(map[string]config.VendorConfig)
			}
			vc, ok := cfg.Vendors[cfg.Vendor]
			if !ok {
				return fmt.Errorf("vendor %q not found", cfg.Vendor)
			}
			if vc.Endpoints == nil {
				vc.Endpoints = make(map[string]config.EndpointConfig)
			}
			ep, ok := vc.Endpoints[cfg.Endpoint]
			if !ok {
				return fmt.Errorf("endpoint %q not found in vendor %q", cfg.Endpoint, cfg.Vendor)
			}
			ep.BaseURL = v
			vc.Endpoints[cfg.Endpoint] = ep
			cfg.Vendors[cfg.Vendor] = vc
		}
	}
	if v, ok := values["maxIterations"].(float64); ok {
		cfg.MaxIterations = int(v)
	}
	if v, ok := values["probeContext"].(bool); ok {
		cfg.ProbeContext = v
	}
	if v, ok := values["impersonatePreset"].(string); ok {
		cfg.Impersonation.Preset = v
	}
	if v, ok := values["impersonateCustomVersion"].(string); ok {
		cfg.Impersonation.CustomVersion = v
	}
	if v, ok := values["streamEncoder"].(string); ok {
		cfg.Stream.HardwareEncoder = v
	}
	if v, ok := values["streamFPS"].(float64); ok {
		cfg.Stream.FPS = int(v)
	}
	if v, ok := values["subAgentMaxConcurrent"].(float64); ok {
		cfg.SubAgents.MaxConcurrent = int(v)
	}
	if v, ok := values["subAgentShowOutput"].(bool); ok {
		cfg.SubAgents.ShowOutput = v
	}
	if v, ok := values["swarmMaxTeammates"].(float64); ok {
		cfg.Swarm.MaxTeammatesPerTeam = int(v)
	}
	if v, ok := values["swarmInboxSize"].(float64); ok {
		cfg.Swarm.InboxSize = int(v)
	}
	if v, ok := values["a2aDisabled"].(bool); ok {
		cfg.A2A.Disabled = v
	}
	if v, ok := values["a2aPort"].(float64); ok {
		cfg.A2A.Port = int(v)
	}
	if v, ok := values["subAgentTimeout"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			// Silent success on a typo'd duration hid the failure (#206).
			return fmt.Errorf("invalid subAgentTimeout %q (use e.g. \"45s\"): %w", v, err)
		}
		cfg.SubAgents.Timeout = d
	}
	if v, ok := values["swarmTimeout"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid swarmTimeout %q (use e.g. \"45s\"): %w", v, err)
		}
		cfg.Swarm.TeammateTimeout = d
	}
	if v, ok := values["a2aHost"].(string); ok {
		cfg.A2A.Host = v
	}
	if v, ok := values["knightEnabled"].(bool); ok {
		cfg.KnightConfig.Enabled = v
	}
	if v, ok := values["knightTrustLevel"].(string); ok {
		cfg.KnightConfig.TrustLevel = v
	}
	if v, ok := values["sidebarVisible"].(bool); ok {
		cfg.UI.SidebarVisible = &v
	}

	if err := cfg.Save(); err != nil {
		return err
	}
	// Save() strips instance-sourced keys from the global file write; persist
	// any such field touched by this update to the instance file too, or the
	// change would be silently lost on restart (#282).
	return persistTouchedInstanceFields(cfg, values)
}

// updateConfigInstanceKeys maps UpdateConfig value keys to the top-level YAML
// config keys they write. Used to detect when an update touches a field that
// Save() would strip from the global file because it originated from the
// instance config.
var updateConfigInstanceKeys = map[string]string{
	"baseURL":                  "vendors",
	"vendor":                   "vendor",
	"endpoint":                 "endpoint",
	"model":                    "model",
	"language":                 "language",
	"extraPrompt":              "system_prompt",
	"defaultMode":              "default_mode",
	"maxIterations":            "max_iterations",
	"probeContext":             "probe_context",
	"impersonatePreset":        "impersonation",
	"impersonateCustomVersion": "impersonation",
	"streamEncoder":            "stream",
	"streamFPS":                "stream",
	"subAgentMaxConcurrent":    "subagents",
	"subAgentShowOutput":       "subagents",
	"subAgentTimeout":          "subagents",
	"swarmMaxTeammates":        "swarm",
	"swarmInboxSize":           "swarm",
	"swarmTimeout":             "swarm",
	"a2aDisabled":              "a2a",
	"a2aPort":                  "a2a",
	"a2aHost":                  "a2a",
	"knightEnabled":            "knight",
	"knightTrustLevel":         "knight",
	"sidebarVisible":           "ui",
}

// persistTouchedInstanceFields re-persists instance-sourced fields that the
// current UpdateConfig call modified. cfg.Save() excludes those keys from the
// global file; without this write-back the in-memory change would only survive
// until restart (#282). It uses SaveInstanceScoped so the sticky save scope is
// left untouched (see SetEndpointLimits).
func persistTouchedInstanceFields(cfg *config.Config, values map[string]interface{}) error {
	if !cfg.HasInstanceConfigAttached() {
		return nil
	}
	touched := make(map[string]bool)
	for key := range values {
		if yk, ok := updateConfigInstanceKeys[key]; ok {
			touched[yk] = true
		}
	}
	if len(touched) == 0 {
		return nil
	}
	for _, yk := range cfg.InstanceFields() {
		if touched[yk] {
			return cfg.SaveInstanceScoped(cfg.InstanceWorkspace())
		}
	}
	return nil
}

// saveWithInstanceWriteback persists cfg and, when the change touched a
// vendor that only exists in the merged (instance-bound) view, mirrors it
// to the instance file and the global snapshot.
//
// cfg.Save() funnels vendors through globalOnlyVendors, which drops every
// vendor absent from globalSnap — so on instance-bound workspaces a NEW
// vendor/endpoint written by AddCustomEndpoint (or a key binding written by
// SaveAPIKey) silently vanished on restart (#368). The instance write-back
// uses marshalInstanceDelta semantics (vendor exists in current but not in
// globalSnap → included), and the snapshot sync keeps future Save() calls
// from stripping it from the vendors.yaml write too.
func saveWithInstanceWriteback(cfg *config.Config, vendor string) error {
	if err := cfg.Save(); err != nil {
		return err
	}
	if !cfg.HasInstanceConfigAttached() {
		return nil
	}
	for _, yk := range cfg.InstanceFields() {
		if yk == "vendors" {
			if err := cfg.SaveInstanceScoped(cfg.InstanceWorkspace()); err != nil {
				return err
			}
			cfg.SyncVendorToGlobalSnapshot(vendor)
			break
		}
	}
	return nil
}

// SaveAPIKey saves an API key for a vendor/endpoint.
func SaveAPIKey(vendor, endpoint, apiKey string) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}

	// Determine scope: if the vendor has multiple endpoints (gateway type),
	// save to endpoint scope; otherwise vendor scope.
	vendorScoped := true
	if vc, ok := cfg.Vendors[vendor]; ok && len(vc.Endpoints) > 1 {
		vendorScoped = false
	}

	if err := cfg.SetEndpointAPIKey(vendor, endpoint, apiKey, vendorScoped); err != nil {
		return err
	}
	return saveWithInstanceWriteback(cfg, vendor)
}

// SaveDefaultMode saves the default permission mode.
func SaveDefaultMode(mode string) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}
	return cfg.SaveDefaultModePreference(mode)
}

func SaveA2AEnabled(enabled bool) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}
	return cfg.SaveA2AEnabled(enabled)
}

// ─── Vendor/Endpoint/Model Helpers ────────────────────────

// VendorPresets returns vendor preset info for onboarding.
type VendorPresetInfo struct {
	ID          string               `json:"id"`
	DisplayName string               `json:"displayName"`
	Endpoints   []EndpointPresetInfo `json:"endpoints"`
}

// EndpointPresetInfo describes an endpoint preset.
type EndpointPresetInfo struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"displayName"`
	Models          []string `json:"models"`
	DefaultEndpoint bool     `json:"defaultEndpoint"`
}

// GetVendorPresets returns vendor presets for onboarding.
func GetVendorPresets() []VendorPresetInfo {
	presets := config.VendorPresets()
	result := make([]VendorPresetInfo, len(presets))
	for i, p := range presets {
		eps := make([]EndpointPresetInfo, len(p.Endpoints))
		for j, ep := range p.Endpoints {
			eps[j] = EndpointPresetInfo{
				ID:              ep.ID,
				DisplayName:     ep.DisplayName,
				Models:          ep.Models,
				DefaultEndpoint: ep.ID == p.DefaultEndpoint,
			}
		}
		result[i] = VendorPresetInfo{
			ID:          p.ID,
			DisplayName: p.DisplayName,
			Endpoints:   eps,
		}
	}
	return result
}

// VendorNames returns available vendor names.
func VendorNames() []string {
	globalMu.RLock()
	defer globalMu.RUnlock()
	cfg := globalCfg
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	var names []string
	for k := range cfg.Vendors {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// EndpointInfo describes a vendor endpoint.
type EndpointInfo struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
}

// EndpointsForVendor returns endpoint info for a vendor.
func EndpointsForVendor(vendor string) []EndpointInfo {
	globalMu.RLock()
	defer globalMu.RUnlock()
	cfg := globalCfg
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	vc, ok := cfg.Vendors[vendor]
	if !ok {
		return nil
	}
	var result []EndpointInfo
	for key, ep := range vc.Endpoints {
		result = append(result, EndpointInfo{
			Key:         key,
			DisplayName: ep.DisplayName,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

// ModelsForEndpoint returns available model names for a vendor and endpoint key.
func ModelsForEndpoint(vendor, endpointKey string) []string {
	globalMu.RLock()
	defer globalMu.RUnlock()
	cfg := globalCfg
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	vc, ok := cfg.Vendors[vendor]
	if !ok {
		return nil
	}
	ep, ok := vc.Endpoints[endpointKey]
	if !ok {
		return nil
	}
	return ep.Models
}

// ─── Impersonation ──────────────────────────────────────

// ImpersonationPresetInfo describes an impersonation preset for the frontend.
type ImpersonationPresetInfo struct {
	ID             string            `json:"id"`
	DisplayName    string            `json:"displayName"`
	DefaultVersion string            `json:"defaultVersion"`
	ExtraHeaders   map[string]string `json:"extraHeaders,omitempty"`
}

// GetImpersonationPresets returns the real presets from provider.DefaultImpersonationPresets().
func GetImpersonationPresets() []ImpersonationPresetInfo {
	presets := provider.DefaultImpersonationPresets()
	result := make([]ImpersonationPresetInfo, len(presets))
	for i, p := range presets {
		result[i] = ImpersonationPresetInfo{
			ID:             p.ID,
			DisplayName:    p.DisplayName,
			DefaultVersion: p.DefaultVersion,
			ExtraHeaders:   p.ExtraHeaders,
		}
	}
	return result
}

// ApplyImpersonation applies an impersonation preset and persists to config.
// #614: an unknown presetID is rejected instead of silently disabling
// impersonation while still persisting the ID (UI/runtime state fork), and
// an empty/nil customHeaders map means "keep existing headers" — the
// frontend submits `{} as Record<string,string>` on a plain preset switch,
// and the old full-struct overwrite wiped user-saved custom headers
// (#67/#69 struct-overwrite family, 4th instance).
func ApplyImpersonation(presetID, version string, customHeaders map[string]string) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}

	if presetID != "none" && presetID != "" && provider.FindPresetByID(presetID) == nil {
		return fmt.Errorf("unknown impersonation preset %q", presetID)
	}

	var preset *provider.ImpersonationPreset
	if presetID != "none" && presetID != "" {
		for _, p := range provider.DefaultImpersonationPresets() {
			if p.ID == presetID {
				preset = &p
				break
			}
		}
	}

	// Merge, don't overwrite: only replace headers when this call actually
	// carries any. Clearing all custom headers is not expressible through
	// this API (an empty map is indistinguishable from "not submitted").
	mergedHeaders := cfg.Impersonation.CustomHeaders
	if len(customHeaders) > 0 {
		mergedHeaders = customHeaders
	}

	provider.SetActiveImpersonation(preset, version, mergedHeaders)

	cfg.Impersonation = config.ImpersonationConfig{
		Preset:        presetID,
		CustomVersion: version,
		CustomHeaders: mergedHeaders,
	}
	return cfg.Save()
}

// Ensure unused imports are referenced.
var (
	_ = time.Duration(0)
	_ = stream.StreamConfig{}
)

// HookConfigJSON is a JSON-serializable wrapper for hooks.HookConfig.
type HookConfigJSON = hooks.HookConfig

// GetHooks returns the current hooks configuration.
func (b *ChatBridge) GetHooks() hooks.HookConfig {
	// globalMu, not b.mu: the same *config.Config is guarded by globalMu in
	// UpdateConfig & co. — two unrelated locks on one pointer allowed
	// concurrent map read/write (fatal, unrecoverable) between cfg.Save()
	// marshaling and cfg.Vendors writes (#205).
	globalMu.RLock()
	defer globalMu.RUnlock()
	if b.cfg == nil {
		return hooks.HookConfig{}
	}
	return b.cfg.Hooks
}

// SaveHooks saves the hooks configuration.
func (b *ChatBridge) SaveHooks(cfg hooks.HookConfig) error {
	// globalMu — see GetHooks (#205).
	globalMu.Lock()
	defer globalMu.Unlock()
	if b.cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	b.cfg.Hooks = cfg
	if b.agent != nil {
		b.agent.SetHookConfig(cfg)
	}
	return b.cfg.Save()
}

// TestHookMatchResult is the result of testing a hook match pattern.
type TestHookMatchResult struct {
	Matched bool   `json:"matched"`
	Error   string `json:"error,omitempty"`
}

// TestHookMatch tests a hook match pattern against a tool name and raw input.
func (b *ChatBridge) TestHookMatch(mode, pattern, toolName, rawInput string) TestHookMatchResult {
	matched, err := hooks.TestMatch(mode, pattern, toolName, rawInput)
	if err != nil {
		return TestHookMatchResult{Matched: false, Error: err.Error()}
	}
	return TestHookMatchResult{Matched: matched}
}

// ─── Custom Endpoint ───────────────────────────────────

// TestEndpointResult is the result of testing an endpoint connection.
type TestEndpointResult struct {
	OK         bool     `json:"ok"`
	Message    string   `json:"message"`
	Models     []string `json:"models,omitempty"`
	ModelCount int      `json:"modelCount"`
}

// TestEndpointConnection tests an endpoint by fetching its model list.
func TestEndpointConnection(protocol, baseURL, apiKey string) (*TestEndpointResult, error) {
	tmpResolved := &config.ResolvedEndpoint{
		Protocol: protocol,
		BaseURL:  baseURL,
	}
	if apiKey != "" {
		tmpResolved.APIKey = apiKey
	}
	models, err := provider.DiscoverModels(context.Background(), tmpResolved)
	if err != nil {
		return &TestEndpointResult{OK: false, Message: "Connection failed: " + err.Error()}, nil
	}
	return &TestEndpointResult{
		OK:         true,
		Message:    fmt.Sprintf("Found %d models", len(models)),
		Models:     models,
		ModelCount: len(models),
	}, nil
}

// AddCustomEndpoint adds a new endpoint to a vendor in the config and saves.
// If an endpoint with the same name already exists, only the provided fields are updated.
func AddCustomEndpoint(vendor, name, protocol, baseURL, apiKey string) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}

	vc, ok := cfg.Vendors[vendor]
	if !ok {
		vc = config.VendorConfig{Endpoints: make(map[string]config.EndpointConfig)}
		cfg.Vendors[vendor] = vc
	}

	// Load existing config and patch only provided fields
	ep := vc.Endpoints[name]
	if protocol != "" {
		ep.Protocol = protocol
	}
	if baseURL != "" {
		ep.BaseURL = baseURL
	}
	if apiKey != "" {
		// Secrets never go to vendors.yaml in plaintext (#250): persist the
		// key to keys.env (0600, managed) and store a ${VAR} reference in the
		// endpoint config. Resolution: config.Load seeds keys.env vars into
		// the process env (env.go loadRuntimeEnv) and ResolveActiveEndpoint
		// expands ${VAR} via ExpandEnv, so the reference resolves at startup.
		// Setenv here makes the key usable immediately in this process too.
		envVar := config.PreferredEndpointAPIKeyEnvVar(vendor, name)
		if err := config.WriteKeysEnv(map[string]string{envVar: apiKey}); err != nil {
			return fmt.Errorf("saving API key: %w", err)
		}
		// #294: the load path (loadKeysEnvInto / loadRuntimeEnv) deliberately
		// does NOT override existing process env — "shell env takes
		// precedence". Unconditionally Setenv here would flip that precedence
		// for this process only, so after a restart the endpoint silently
		// resolves back to the shell-exported (stale) key. Instead, only
		// seed the env when the variable is not already shell-exported; if it
		// is, warn so the user knows the shell export shadows the saved key.
		if existing, ok := os.LookupEnv(envVar); ok && existing != apiKey {
			debug.Log("config", "env %s is shell-exported with a different value; the saved key will not take effect until the shell export is removed (#294)", envVar)
		} else if !ok {
			os.Setenv(envVar, apiKey)
		}
		ep.APIKey = "${" + envVar + "}"
	}
	// An empty apiKey leaves the stored value untouched: the frontend's add/
	// edit form submits an empty key when the user did not (re)enter one, so
	// clearing here would wipe keys on unrelated edits. Legacy plaintext keys
	// already on disk are migrated to keys.env by Save() via
	// MigrateVendorsFilePlaintextAPIKeys.
	if name != "" {
		ep.DisplayName = name
	}
	vc.Endpoints[name] = ep
	cfg.Vendors[vendor] = vc
	return saveWithInstanceWriteback(cfg, vendor)
}

// ─── Resolved Endpoint Info ─────────────────────────────

// ResolvedEndpointInfo provides the current resolved LLM endpoint details for the frontend.
type ResolvedEndpointInfo struct {
	VendorID       string   `json:"vendorId"`
	VendorName     string   `json:"vendorName"`
	EndpointID     string   `json:"endpointId"`
	EndpointName   string   `json:"endpointName"`
	Protocol       string   `json:"protocol"`
	BaseURL        string   `json:"baseUrl"`
	APIKeySet      bool     `json:"apiKeySet"`
	APIKeyMasked   string   `json:"apiKeyMasked"`
	Model          string   `json:"model"`
	Models         []string `json:"models"`
	ContextWindow  int      `json:"contextWindow"`
	SupportsVision bool     `json:"supportsVision"`
}

// GetResolvedEndpoint returns the currently resolved active endpoint info.
func GetResolvedEndpoint() (*ResolvedEndpointInfo, error) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	cfg := globalCfg
	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		return nil, err
	}

	return &ResolvedEndpointInfo{
		VendorID:       resolved.VendorID,
		VendorName:     resolved.VendorName,
		EndpointID:     resolved.EndpointID,
		EndpointName:   resolved.EndpointName,
		Protocol:       resolved.Protocol,
		BaseURL:        resolved.BaseURL,
		APIKeySet:      resolved.APIKey != "",
		APIKeyMasked:   maskAPIKey(resolved.APIKey),
		Model:          resolved.Model,
		Models:         resolved.Models,
		ContextWindow:  resolved.ContextWindow,
		SupportsVision: resolved.SupportsVision,
	}, nil
}

// maskAPIKey returns a masked version of the API key for display.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "***"
	}
	return string([]rune(key)[:3]) + "***" + string([]rune(key)[len([]rune(key))-3:])
}

// FetchModelsForEndpoint dynamically discovers models from an API endpoint.
// If the target endpoint fails, tries other endpoints with the same BaseURL
// within the same vendor. Only reports error if ALL same-domain endpoints fail.
func FetchModelsForEndpoint(vendor, endpoint, apiKey, baseURL string) ([]string, error) {
	// Snapshot everything needed under RLock, then release BEFORE any
	// network I/O: holding globalMu.RLock across 30s DiscoverModels calls
	// let one queued writer block every later reader — settings page
	// freeze up to 30s (#204).
	globalMu.RLock()
	cfg := globalCfg

	if cfg == nil {
		globalMu.RUnlock()
		return nil, fmt.Errorf("config not loaded")
	}

	vc, ok := cfg.Vendors[vendor]
	if !ok {
		globalMu.RUnlock()
		return nil, fmt.Errorf("vendor %q not found", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		globalMu.RUnlock()
		return nil, fmt.Errorf("endpoint %q not found", endpoint)
	}

	protocol := ep.Protocol
	if baseURL == "" {
		baseURL = ep.BaseURL
	}

	// Auto-resolve API key if not provided
	if apiKey == "" {
		apiKey = resolveAPIKey(cfg, vendor, endpoint)
	}

	// Pre-resolve same-domain fallback endpoints while still locked.
	type fallbackEP struct {
		name, protocol, baseURL, apiKey string
	}
	var fallbacks []fallbackEP
	for epName, epCfg := range vc.Endpoints {
		if epName == endpoint || epCfg.BaseURL != baseURL {
			continue
		}
		epKey := resolveAPIKey(cfg, vendor, epName)
		if epKey == "" {
			continue
		}
		fallbacks = append(fallbacks, fallbackEP{epName, epCfg.Protocol, epCfg.BaseURL, epKey})
	}
	globalMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try the requested endpoint first
	tmpResolved := &config.ResolvedEndpoint{
		VendorID: vendor, EndpointID: endpoint,
		Protocol: protocol, BaseURL: baseURL, APIKey: apiKey,
	}
	models, err := provider.DiscoverModels(ctx, tmpResolved)
	if err == nil && len(models) > 0 {
		return models, nil
	}

	// Failed — try other endpoints with the same BaseURL (same domain)
	var errs []string
	if err != nil {
		errs = append(errs, fmt.Sprintf("%s: %v", endpoint, err))
	}
	for _, fb := range fallbacks {
		epResolved := &config.ResolvedEndpoint{
			VendorID: vendor, EndpointID: fb.name,
			Protocol: fb.protocol, BaseURL: fb.baseURL, APIKey: fb.apiKey,
		}
		epModels, epErr := provider.DiscoverModels(ctx, epResolved)
		if epErr == nil && len(epModels) > 0 {
			return epModels, nil
		}
		if epErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", fb.name, epErr))
		}
	}

	if len(errs) == 0 {
		return nil, fmt.Errorf("no models found for %s", baseURL)
	}
	return nil, fmt.Errorf("all endpoints for %s failed: %s", baseURL, strings.Join(errs, "; "))
}

// resolveAPIKey mimics the resolve chain: endpoint key → vendor key → expand env vars.
// This avoids calling ResolveEndpoint which requires a model.
func resolveAPIKey(cfg *config.Config, vendor, endpoint string) string {
	vc, ok := cfg.Vendors[vendor]
	if !ok {
		return ""
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return ""
	}
	// endpoint key first, then vendor key
	key := strings.TrimSpace(ep.APIKey)
	if key == "" {
		key = strings.TrimSpace(vc.APIKey)
	}
	return config.ExpandEnv(key)
}

// EndpointDetails provides detailed info about a configured endpoint.
type EndpointDetails struct {
	DisplayName    string   `json:"displayName"`
	Protocol       string   `json:"protocol"`
	BaseURL        string   `json:"baseUrl"`
	APIKeySet      bool     `json:"apiKeySet"`
	APIKeyMasked   string   `json:"apiKeyMasked"`
	DefaultModel   string   `json:"defaultModel"`
	Models         []string `json:"models"`
	ContextWindow  int      `json:"contextWindow"`
	MaxTokens      int      `json:"maxTokens"`
	AuthType       string   `json:"authType"`
	SupportsVision bool     `json:"supportsVision"`
}

// GetEndpointDetails returns details for a specific vendor endpoint.
func GetEndpointDetails(vendor, endpoint string) *EndpointDetails {
	globalMu.RLock()
	defer globalMu.RUnlock()
	cfg := globalCfg
	if cfg == nil {
		return nil // read op — empty value is the established convention
	}
	vc, ok := cfg.Vendors[vendor]
	if !ok {
		return nil
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return nil
	}

	// Resolve API key: endpoint → vendor → env vars (same chain as runtime)
	apiKey := resolveAPIKey(cfg, vendor, endpoint)

	return &EndpointDetails{
		DisplayName:    ep.DisplayName,
		Protocol:       ep.Protocol,
		BaseURL:        ep.BaseURL,
		APIKeySet:      apiKey != "",
		APIKeyMasked:   maskAPIKey(apiKey),
		DefaultModel:   ep.DefaultModel,
		Models:         ep.Models,
		ContextWindow:  ep.ContextWindow,
		MaxTokens:      ep.MaxTokens,
		AuthType:       ep.AuthType,
		SupportsVision: ep.SupportsVision != nil && *ep.SupportsVision,
	}
}

// persistLimitChange persists a vendor limit edit made in the settings UI.
//
// When an instance config is attached, the delta goes to the instance override
// file via SaveInstanceScoped WITHOUT changing the sticky save scope: using
// SaveScoped("instance") here would redirect all subsequent scope-aware saves
// (Save*Preference, PatchIMAdapter) to the instance file for the lifetime of
// this long-held shared config (#282). This path also avoids the full
// Validate() which requires a non-empty model — limit changes don't affect
// model validity.
//
// When NO instance config is attached, InstanceWorkspace() is "" and the
// previously unconditional SaveInstanceScoped("") returned nil while
// silently writing to a garbage instances/e3b0c442… (sha256 of empty string)
// directory no one parses — the edit was lost on restart (#532). Fall back to
// the global Save(), the same no-instance path saveWithInstanceWriteback uses.
func persistLimitChange(cfg *config.Config) error {
	if cfg.HasInstanceConfigAttached() {
		return cfg.SaveInstanceScoped(cfg.InstanceWorkspace())
	}
	return cfg.Save()
}

// SetEndpointLimits updates context_window and max_tokens for a vendor/endpoint.
// A value of 0 means "auto" (clears the override).
func SetEndpointLimits(vendor, endpoint string, contextWindow, maxTokens int) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalCfg == nil {
		return fmt.Errorf("config not initialized")
	}
	vc, ok := globalCfg.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q not found", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return fmt.Errorf("endpoint %q not found in vendor %q", endpoint, vendor)
	}
	ep.ContextWindow = contextWindow
	ep.MaxTokens = maxTokens
	vc.Endpoints[endpoint] = ep
	globalCfg.Vendors[vendor] = vc
	return persistLimitChange(globalCfg)
}

// SetModelLimits updates per-model context_window and max_tokens overrides
// for a vendor/endpoint/model combination. A value of 0 means "auto" (clears
// the override for that field, falling back to endpoint-level or inference).
func SetModelLimits(vendor, endpoint, model string, contextWindow, maxTokens int) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalCfg == nil {
		return fmt.Errorf("config not initialized")
	}
	vc, ok := globalCfg.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q not found", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return fmt.Errorf("endpoint %q not found in vendor %q", endpoint, vendor)
	}
	if ep.ModelLimits == nil {
		ep.ModelLimits = make(map[string]config.ModelLimitConfig)
	}
	ml := ep.ModelLimits[model]
	ml.ContextWindow = contextWindow
	ml.MaxTokens = maxTokens
	if contextWindow == 0 && maxTokens == 0 {
		delete(ep.ModelLimits, model)
	} else {
		ep.ModelLimits[model] = ml
	}
	vc.Endpoints[endpoint] = ep
	globalCfg.Vendors[vendor] = vc
	// Non-sticky instance save with no-instance fallback — see
	// persistLimitChange (#282, #532).
	return persistLimitChange(globalCfg)
}

// ModelLimitInfo represents per-model limit overrides for the frontend.
type ModelLimitInfo struct {
	Model         string `json:"model"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
}

// GetModelLimits returns all per-model limit overrides for a vendor/endpoint.
func GetModelLimits(vendor, endpoint string) []ModelLimitInfo {
	globalMu.RLock()
	defer globalMu.RUnlock()
	if globalCfg == nil {
		return nil
	}
	vc, ok := globalCfg.Vendors[vendor]
	if !ok {
		return nil
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok || ep.ModelLimits == nil {
		return nil
	}
	var result []ModelLimitInfo
	for model, ml := range ep.ModelLimits {
		result = append(result, ModelLimitInfo{
			Model:         model,
			ContextWindow: ml.ContextWindow,
			MaxTokens:     ml.MaxTokens,
		})
	}
	return result
}

// AnthropicOAuthStatus reports whether the stored Anthropic OAuth token is
// usable OR recoverable (#599 O2). Previously it returned raw
// HasUsableToken: in the ~5.5-minute window between IsExpired (5-min
// early) and HasUsableToken (+30s grace), and worse — for any expired
// token with an intact RefreshToken — the UI flipped to "not connected",
// nudging users through a full browser re-auth when a single silent
// refresh would recover. Return true when the token is valid OR
// refreshable (refresh token present); "dead" (no refresh token, no
// usable access token) still reports false so the UI keeps prompting
// for a real re-auth.
func AnthropicOAuthStatus() bool {
	store := auth.DefaultStore()
	usable, err := store.HasUsableToken(auth.ProviderAnthropic)
	if err == nil && usable {
		return true // valid
	}
	// Refreshable probe: a stored token with an access token AND a refresh
	// token can self-recover — count it as connected.
	if info, ierr := store.Load(auth.ProviderAnthropic); ierr == nil && info != nil {
		if strings.TrimSpace(info.AccessToken) != "" && strings.TrimSpace(info.RefreshToken) != "" {
			return true // refreshable
		}
	}
	return false // dead
}

// StartAnthropicOAuth initiates the OAuth flow and returns the URL for the user to visit.
func StartAnthropicOAuth() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flow, err := auth.StartClaudeOAuthFlow(ctx)
	if err != nil {
		return "", fmt.Errorf("starting OAuth flow: %w", err)
	}
	// Store flow for completion, closing any previous unfinished flow to
	// avoid leaking its callback HTTP listener and receiver goroutine.
	oauthMu.Lock()
	if currentOAuthFlow != nil {
		currentOAuthFlow.Close()
	}
	currentOAuthFlow = flow
	oauthMu.Unlock()
	return flow.AutoURL, nil
}

// CompleteAnthropicOAuth blocks until the OAuth callback is received and the token is saved.
// Should be called from a goroutine after the browser is opened.
func CompleteAnthropicOAuth() error {
	oauthMu.Lock()
	flow := currentOAuthFlow
	oauthMu.Unlock()
	if flow == nil {
		return fmt.Errorf("no OAuth flow in progress")
	}
	defer func() {
		oauthMu.Lock()
		flow.Close()
		// Only clear the current flow if it is still THIS flow (#295): a
		// newer login may have replaced it while we were waiting, and
		// clearing unconditionally would kill the new flow's login path.
		if currentOAuthFlow == flow {
			currentOAuthFlow = nil
		}
		oauthMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	code, isAutomatic, err := auth.WaitForClaudeAuthCode(ctx, flow)
	if err != nil {
		return fmt.Errorf("waiting for auth code: %w", err)
	}
	_ = isAutomatic

	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	tokenResp, err := auth.ExchangeClaudeCodeForTokens(ctx2, code, flow.CodeVerifier, !isAutomatic, flow.Port)
	if err != nil {
		return fmt.Errorf("exchanging token: %w", err)
	}

	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	info := &auth.Info{
		ProviderID:   auth.ProviderAnthropic,
		Type:         "oauth",
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	if err := auth.DefaultStore().Save(info); err != nil {
		return err
	}
	// #616: the token is only read when the provider is (re)created via
	// ResolveActiveEndpoint. Without this refresh the running provider keeps
	// its old auth state and chats keep 401-ing until restart, while the UI
	// (which reads the store via AnthropicOAuthStatus) shows "connected".
	// Symmetric with App.UpdateConfig's post-save OnConfigProviderChanged.
	refreshRunningProviderAfterAuth()
	return nil
}

// refreshRunningProviderAfterAuth nudges the active bridge to rebuild its
// provider so a freshly persisted credential is picked up immediately
// instead of on the next config save or app restart (#616).
func refreshRunningProviderAfterAuth() {
	if bridge := GetChatBridge(); bridge != nil {
		bridge.OnConfigProviderChanged()
	}
}

// LogoutAnthropicOAuth removes the stored Anthropic OAuth token.
func LogoutAnthropicOAuth() error {
	return auth.DefaultStore().Delete(auth.ProviderAnthropic)
}

var (
	oauthMu          sync.Mutex
	currentOAuthFlow *auth.ClaudeOAuthFlow
)
