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

	"github.com/topcheer/ggcode/internal/config"
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

	A2ALANDiscovery bool `json:"a2aLanDiscovery"`

	// Harness
	HarnessAutoRun  string `json:"harnessAutoRun"`
	HarnessAutoInit bool   `json:"harnessAutoInit"`

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
	cfg := globalCfg
	globalMu.RUnlock()

	if cfg == nil {
		return &FullConfig{NeedsSetup: true}, nil
	}

	// Check if API key is set (without exposing it)
	apiKeySet := false
	if vc, ok := cfg.Vendors[cfg.Vendor]; ok {
		if ep, ok := vc.Endpoints[cfg.Endpoint]; ok {
			apiKeySet = ep.APIKey != ""
		}
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

		A2ALANDiscovery: cfg.A2A.LANDiscovery,

		HarnessAutoRun:  cfg.Harness.AutoRun,
		HarnessAutoInit: cfg.Harness.AutoInit,

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
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return nil
	}

	if v, ok := values["vendor"].(string); ok {
		cfg.Vendor = v
	}
	if v, ok := values["endpoint"].(string); ok {
		cfg.Endpoint = v
	}
	if v, ok := values["model"].(string); ok {
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
			vc := cfg.Vendors[cfg.Vendor]
			if vc.Endpoints == nil {
				vc.Endpoints = make(map[string]config.EndpointConfig)
			}
			ep := vc.Endpoints[cfg.Endpoint]
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
	if v, ok := values["harnessAutoRun"].(string); ok {
		cfg.Harness.AutoRun = v
	}
	if v, ok := values["harnessAutoInit"].(bool); ok {
		cfg.Harness.AutoInit = v
	}

	return cfg.Save()
}

// SaveAPIKey saves an API key for a vendor/endpoint.
func SaveAPIKey(vendor, endpoint, apiKey string) error {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return nil
	}

	// Determine scope: if the vendor has multiple endpoints (gateway type),
	// save to endpoint scope; otherwise vendor scope.
	vendorScoped := true
	if vc, ok := cfg.Vendors[vendor]; ok && len(vc.Endpoints) > 1 {
		vendorScoped = false
	}

	cfg.SetEndpointAPIKey(vendor, endpoint, apiKey, vendorScoped)
	return cfg.Save()
}

// SaveDefaultMode saves the default permission mode.
func SaveDefaultMode(mode string) error {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return nil
	}
	return cfg.SaveDefaultModePreference(mode)
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
	cfg := globalCfg
	globalMu.RUnlock()
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
	cfg := globalCfg
	globalMu.RUnlock()
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
	cfg := globalCfg
	globalMu.RUnlock()
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
func ApplyImpersonation(presetID, version string, customHeaders map[string]string) error {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return nil
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

	provider.SetActiveImpersonation(preset, version, customHeaders)

	cfg.Impersonation = config.ImpersonationConfig{
		Preset:        presetID,
		CustomVersion: version,
		CustomHeaders: customHeaders,
	}
	return cfg.Save()
}

// Ensure unused imports are referenced.
var (
	_ = time.Duration(0)
	_ = hooks.HookConfig{}
	_ = stream.StreamConfig{}
)

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
func AddCustomEndpoint(vendor, name, protocol, baseURL, apiKey string) error {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return nil
	}

	vc, ok := cfg.Vendors[vendor]
	if !ok {
		vc = config.VendorConfig{Endpoints: make(map[string]config.EndpointConfig)}
		cfg.Vendors[vendor] = vc
	}

	vc.Endpoints[name] = config.EndpointConfig{
		DisplayName: name,
		Protocol:    protocol,
		BaseURL:     baseURL,
		APIKey:      apiKey,
	}
	cfg.Vendors[vendor] = vc
	return cfg.Save()
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
	cfg := globalCfg
	globalMu.RUnlock()
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
	return key[:3] + "***" + key[len(key)-3:]
}

// FetchModelsForEndpoint dynamically discovers models from an API endpoint.
// If the target endpoint fails, tries other endpoints with the same BaseURL
// within the same vendor. Only reports error if ALL same-domain endpoints fail.
func FetchModelsForEndpoint(vendor, endpoint, apiKey, baseURL string) ([]string, error) {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()

	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}

	vc, ok := cfg.Vendors[vendor]
	if !ok {
		return nil, fmt.Errorf("vendor %q not found", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
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
	for epName, epCfg := range vc.Endpoints {
		if epName == endpoint {
			continue
		}
		if epCfg.BaseURL != baseURL {
			continue
		}
		epKey := resolveAPIKey(cfg, vendor, epName)
		if epKey == "" {
			continue
		}
		epResolved := &config.ResolvedEndpoint{
			VendorID: vendor, EndpointID: epName,
			Protocol: epCfg.Protocol, BaseURL: epCfg.BaseURL, APIKey: epKey,
		}
		epModels, epErr := provider.DiscoverModels(ctx, epResolved)
		if epErr == nil && len(epModels) > 0 {
			return epModels, nil
		}
		if epErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", epName, epErr))
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
	SupportsVision bool     `json:"supportsVision"`
}

// GetEndpointDetails returns details for a specific vendor endpoint.
func GetEndpointDetails(vendor, endpoint string) *EndpointDetails {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return nil
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
		SupportsVision: ep.SupportsVision != nil && *ep.SupportsVision,
	}
}
