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
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/stream"
)

var (
	globalCfg *config.Config
	globalMu  sync.RWMutex
)

func SetConfig(cfg *config.Config) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalCfg = cfg

	if st, err := os.Stat(trackedConfigPathLocked()); err == nil {
		cfgFileLastMod, cfgFileLastModOnce = st.ModTime(), true
	} else {
		cfgFileLastModOnce = false
	}
	startConfigFileSync()
}

func GetGlobalConfig() *config.Config {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalCfg
}

var (
	cfgFileSyncOnce    sync.Once
	cfgFileLastMod     time.Time
	cfgFileLastModOnce bool
)

func startConfigFileSync() {
	cfgFileSyncOnce.Do(func() {
		if st, err := os.Stat(trackedConfigPathLocked()); err == nil {
			cfgFileLastMod, cfgFileLastModOnce = st.ModTime(), true
		}
		safego.Go("desktop.wailskit.configFileSync", func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				globalMu.Lock()
				syncCfgFileLocked()
				globalMu.Unlock()
			}
		})
	})
}

func trackedConfigPathLocked() string {
	if globalCfg != nil && globalCfg.FilePath != "" {
		return globalCfg.FilePath
	}
	return config.ConfigPath()
}

func syncCfgFileLocked() {
	path := trackedConfigPathLocked()
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	if cfgFileLastModOnce && !st.ModTime().After(cfgFileLastMod) {
		return
	}
	cfgFileLastMod, cfgFileLastModOnce = st.ModTime(), true
	if globalCfg == nil {
		return
	}

	fresh, lerr := loadCfgMergedWithInstance(path, globalCfg)
	if lerr == nil && fresh != nil {
		globalCfg = fresh
		debug.Log("desktop", "config file changed externally; global config refreshed (#1847, merged view #3769)")
	}
}

func loadCfgMergedWithInstance(path string, runtime *config.Config) (*config.Config, error) {
	if runtime != nil && runtime.HasInstanceConfigAttached() {
		return config.LoadWithInstance(path, runtime.InstanceWorkspace())
	}
	return config.Load(path)
}

func noteConfigFileSaved() {
	if st, err := os.Stat(trackedConfigPathLocked()); err == nil {
		cfgFileLastMod, cfgFileLastModOnce = st.ModTime(), true
	}
}

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

func LoadConfigForWorkspace(workDir string) (*config.Config, error) {
	cfgPath := ResolveConfigFilePath(workDir)
	return config.LoadWithInstance(cfgPath, workDir)
}

func NeedsOnboard() bool {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return true
	}
	return cfg.NeedsOnboard()
}

type FullConfig struct {
	Vendor      string `json:"vendor"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	APIKeySet   bool   `json:"apiKeySet"`
	Language    string `json:"language"`
	ExtraPrompt string `json:"extraPrompt"`
	DefaultMode string `json:"defaultMode"`
	MaxIter     int    `json:"maxIterations"`
	ProbeCtx    bool   `json:"probeContext"`

	ImpersonatePreset        string            `json:"impersonatePreset"`
	ImpersonateCustomVersion string            `json:"impersonateCustomVersion"`
	ImpersonateCustomHeaders map[string]string `json:"impersonateCustomHeaders"`

	SubAgentMaxConcurrent int    `json:"subAgentMaxConcurrent"`
	SubAgentTimeout       string `json:"subAgentTimeout"`
	SubAgentShowOutput    bool   `json:"subAgentShowOutput"`

	SwarmMaxTeammates int    `json:"swarmMaxTeammates"`
	SwarmTimeout      string `json:"swarmTimeout"`
	SwarmInboxSize    int    `json:"swarmInboxSize"`

	A2ADisabled bool   `json:"a2aDisabled"`
	A2APort     int    `json:"a2aPort"`
	A2AHost     string `json:"a2aHost"`

	StreamEncoder string `json:"streamEncoder"`
	StreamFPS     int    `json:"streamFPS"`

	KnightEnabled    bool   `json:"knightEnabled"`
	KnightTrustLevel string `json:"knightTrustLevel"`

	SidebarVisible *bool `json:"sidebarVisible"`

	WorkDir string `json:"workDir"`

	NeedsSetup bool `json:"needsSetup"`
}

func GetFullConfig() (*FullConfig, error) {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()

	if cfg == nil {
		return &FullConfig{NeedsSetup: true}, nil
	}

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

func UpdateConfig(values map[string]interface{}) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}

	if v, ok := values["subAgentTimeout"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {

			return fmt.Errorf("invalid subAgentTimeout %q (use e.g. \"45s\"): %w", v, err)
		}
		_ = d
	}
	if v, ok := values["swarmTimeout"].(string); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid swarmTimeout %q (use e.g. \"45s\"): %w", v, err)
		}
		_ = d
	}

	_, hasBaseURL := values["baseURL"].(string)
	_, hasVendor := values["vendor"].(string)
	_, hasEndpoint := values["endpoint"].(string)
	if hasBaseURL || hasVendor || hasEndpoint {

		vendor, endpoint := cfg.Vendor, cfg.Endpoint
		if nv, ok := values["vendor"].(string); ok && nv != "" {
			vendor = nv
		}
		if ne, ok := values["endpoint"].(string); ok && ne != "" {
			endpoint = ne
		}
		if vendor != "" && endpoint != "" {
			vc, ok := cfg.Vendors[vendor]
			if !ok {
				return fmt.Errorf("vendor %q not found", vendor)
			}
			ep, ok := vc.Endpoints[endpoint]
			if !ok {
				return fmt.Errorf("endpoint %q not found in vendor %q", endpoint, vendor)
			}

			if strings.TrimSpace(ep.Protocol) == "" {
				return fmt.Errorf("endpoint %q for vendor %q must declare a protocol", endpoint, vendor)
			}
		}
	}

	if v, ok := values["impersonatePreset"].(string); ok {
		if v != "none" && v != "" && provider.FindPresetByID(v) == nil {
			return fmt.Errorf("unknown impersonation preset %q", v)
		}
	}

	if v, ok := values["defaultMode"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "supervised", "auto", "bypass", "autopilot":
		default:
			return fmt.Errorf("defaultMode %q must be supervised or bypass (legacy aliases auto/autopilot normalize)", v)
		}
	}
	if v, ok := values["a2aPort"].(float64); ok && v < 0 {
		return fmt.Errorf("a2aPort must not be negative")
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

		cfg.DefaultMode = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := values["baseURL"].(string); ok && v != "" {

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
	impersonationRequested := false
	var pendingImpersonationPreset *provider.ImpersonationPreset

	if cv, ok := values["impersonateCustomVersion"].(string); ok {
		cfg.Impersonation.CustomVersion = cv
	}
	if v, ok := values["impersonatePreset"].(string); ok {

		var preset *provider.ImpersonationPreset
		if v != "none" && v != "" {
			for _, p := range provider.DefaultImpersonationPresets() {
				if p.ID == v {
					preset = &p
					break
				}
			}
		}

		impersonationRequested = true
		pendingImpersonationPreset = preset
		cfg.Impersonation.Preset = v
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

		d, _ := time.ParseDuration(v)
		cfg.SubAgents.Timeout = d
	}
	if v, ok := values["swarmTimeout"].(string); ok {

		d, _ := time.ParseDuration(v)
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

	if impersonationRequested {
		provider.SetActiveImpersonation(pendingImpersonationPreset, cfg.Impersonation.CustomVersion, cfg.Impersonation.CustomHeaders)
	}
	noteConfigFileSaved()

	return persistTouchedInstanceFields(cfg, values)
}

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

func saveWithInstanceWriteback(cfg *config.Config, vendor string) error {
	if err := cfg.Save(); err != nil {
		return err
	}

	noteConfigFileSaved()
	if !cfg.HasInstanceConfigAttached() {
		return nil
	}

	if cfg.VendorNotInGlobalSnapshot(vendor) {
		if err := cfg.SaveInstanceScoped(cfg.InstanceWorkspace()); err != nil {
			return err
		}
		cfg.SyncVendorToGlobalSnapshot(vendor)
	}
	return nil
}

func SaveAPIKey(vendor, endpoint, apiKey string) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}

	vendorScoped := true
	if vc, ok := cfg.Vendors[vendor]; ok && len(vc.Endpoints) > 1 {
		vendorScoped = false
	}

	if err := cfg.SetEndpointAPIKey(vendor, endpoint, apiKey, vendorScoped); err != nil {
		return err
	}
	return saveWithInstanceWriteback(cfg, vendor)
}

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

type VendorPresetInfo struct {
	ID          string               `json:"id"`
	DisplayName string               `json:"displayName"`
	Endpoints   []EndpointPresetInfo `json:"endpoints"`
}

type EndpointPresetInfo struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"displayName"`
	Models          []string `json:"models"`
	DefaultEndpoint bool     `json:"defaultEndpoint"`
}

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

type EndpointInfo struct {
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
}

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

type ImpersonationPresetInfo struct {
	ID             string            `json:"id"`
	DisplayName    string            `json:"displayName"`
	DefaultVersion string            `json:"defaultVersion"`
	ExtraHeaders   map[string]string `json:"extraHeaders,omitempty"`
}

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

func ApplyImpersonation(presetID, version string, customHeaders map[string]string) error {
	globalMu.Lock()
	chatSnap := GetChatBridge()
	err := applyImpersonationLocked(presetID, version, customHeaders)
	globalMu.Unlock()
	if err != nil {
		return err
	}

	if chatSnap != nil {
		chatSnap.refreshRuntimeImpersonationHeaders()
	}
	return nil
}

func applyImpersonationLocked(presetID, version string, customHeaders map[string]string) error {
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

	mergedHeaders := cfg.Impersonation.CustomHeaders
	if len(customHeaders) > 0 {
		mergedHeaders = customHeaders
	}

	cfg.Impersonation = config.ImpersonationConfig{
		Preset:        presetID,
		CustomVersion: version,
		CustomHeaders: mergedHeaders,
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	provider.SetActiveImpersonation(preset, version, mergedHeaders)
	noteConfigFileSaved()

	if err := persistTouchedInstanceFields(cfg, map[string]interface{}{
		"impersonatePreset":        presetID,
		"impersonateCustomVersion": version,
	}); err != nil {
		return err
	}
	return nil
}

func (b *ChatBridge) refreshRuntimeImpersonationHeaders() {
	b.mu.Lock()
	ag := b.agent
	cfg := b.cfg
	b.mu.Unlock()
	if ag == nil || cfg == nil {
		return
	}
	p := ag.Provider()
	if p == nil {
		return
	}
	protocol := "openai"
	if ep := cfg.ActiveEndpointConfig(); ep != nil && ep.Protocol != "" {
		protocol = ep.Protocol
	}
	headers := provider.BuildHeadersForProvider(protocol)
	if mutable, ok := p.(provider.HeaderMutable); ok {
		mutable.UpdateRuntimeHeaders(headers)
	}

	if copilot, ok := p.(interface{ SetImpersonatedUA(string) }); ok {
		if ua := headers.Get("User-Agent"); ua != "" {
			copilot.SetImpersonatedUA(ua)
		}
	}
}

var (
	_ = time.Duration(0)
	_ = stream.StreamConfig{}
)

type HookConfigJSON = hooks.HookConfig

func (b *ChatBridge) GetHooks() hooks.HookConfig {

	globalMu.RLock()
	defer globalMu.RUnlock()
	if b.cfg == nil {
		return hooks.HookConfig{}
	}
	return b.cfg.Hooks
}

func (b *ChatBridge) SaveHooks(cfg hooks.HookConfig) error {

	globalMu.Lock()
	defer globalMu.Unlock()
	if b.cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	b.cfg.Hooks = cfg
	if b.agent != nil {
		b.agent.SetHookConfig(cfg)
	}
	err := b.cfg.Save()
	if err != nil {
		return err
	}

	if b.cfg.HasInstanceConfigAttached() {
		for _, yk := range b.cfg.InstanceFields() {
			if yk == "hooks" {
				if err := b.cfg.SaveInstanceScoped(b.cfg.InstanceWorkspace()); err != nil {
					return err
				}
				break
			}
		}
	}
	noteConfigFileSaved()
	return nil
}

type TestHookMatchResult struct {
	Matched bool   `json:"matched"`
	Error   string `json:"error,omitempty"`
}

func (b *ChatBridge) TestHookMatch(mode, pattern, toolName, rawInput string) TestHookMatchResult {
	matched, err := hooks.TestMatch(mode, pattern, toolName, rawInput)
	if err != nil {
		return TestHookMatchResult{Matched: false, Error: err.Error()}
	}
	return TestHookMatchResult{Matched: matched}
}

type TestEndpointResult struct {
	OK         bool     `json:"ok"`
	Message    string   `json:"message"`
	Models     []string `json:"models,omitempty"`
	ModelCount int      `json:"modelCount"`
}

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

func AddCustomEndpoint(vendor, name, protocol, baseURL, apiKey string) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	cfg := globalCfg
	if cfg == nil {
		return fmt.Errorf("config not initialized")
	}

	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("endpoint name must not be empty")
	}

	vc, ok := cfg.Vendors[vendor]
	if !ok {
		vc = config.VendorConfig{Endpoints: make(map[string]config.EndpointConfig)}

	}

	ep := vc.Endpoints[name]
	if protocol != "" {
		ep.Protocol = protocol
	}
	if baseURL != "" {
		ep.BaseURL = baseURL
	}
	if apiKey != "" {

		envVar := config.PreferredEndpointAPIKeyEnvVar(vendor, name)
		if err := config.WriteKeysEnv(map[string]string{envVar: apiKey}); err != nil {
			return fmt.Errorf("saving API key: %w", err)
		}

		if existing, ok := os.LookupEnv(envVar); ok && existing != apiKey {
			debug.Log("config", "env %s is shell-exported with a different value; the saved key will not take effect until the shell export is removed (#294)", envVar)
		} else if !ok {
			os.Setenv(envVar, apiKey)
		}
		ep.APIKey = "${" + envVar + "}"
	}

	ep.DisplayName = name

	vc.Endpoints[name] = ep
	cfg.Vendors[vendor] = vc
	return saveWithInstanceWriteback(cfg, vendor)
}

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

func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "***"
	}
	return string([]rune(key)[:3]) + "***" + string([]rune(key)[len([]rune(key))-3:])
}

func FetchModelsForEndpoint(vendor, endpoint, apiKey, baseURL string) ([]string, error) {

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

	if apiKey == "" {
		apiKey = resolveAPIKey(cfg, vendor, endpoint)
	}

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

	tmpResolved := &config.ResolvedEndpoint{
		VendorID: vendor, EndpointID: endpoint,
		Protocol: protocol, BaseURL: baseURL, APIKey: apiKey,
	}
	models, err := provider.DiscoverModels(ctx, tmpResolved)
	if err == nil && len(models) > 0 {
		return models, nil
	}

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

func resolveAPIKey(cfg *config.Config, vendor, endpoint string) string {
	vc, ok := cfg.Vendors[vendor]
	if !ok {
		return ""
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return ""
	}

	key := strings.TrimSpace(ep.APIKey)
	if key == "" {
		key = strings.TrimSpace(vc.APIKey)
	}
	return config.ExpandEnv(key)
}

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

func GetEndpointDetails(vendor, endpoint string) *EndpointDetails {
	globalMu.RLock()
	defer globalMu.RUnlock()
	cfg := globalCfg
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

func persistLimitChange(cfg *config.Config) error {

	defer noteConfigFileSaved()
	if cfg.HasInstanceConfigAttached() {
		return cfg.SaveInstanceScoped(cfg.InstanceWorkspace())
	}
	return cfg.Save()
}

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

	return persistLimitChange(globalCfg)
}

type ModelLimitInfo struct {
	Model         string `json:"model"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
}

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

func AnthropicOAuthStatus() bool {
	store := auth.DefaultStore()
	usable, err := store.HasUsableToken(auth.ProviderAnthropic)
	if err == nil && usable {
		return true
	}

	if info, ierr := store.Load(auth.ProviderAnthropic); ierr == nil && info != nil {
		if strings.TrimSpace(info.AccessToken) != "" && strings.TrimSpace(info.RefreshToken) != "" {
			return true
		}
	}
	return false
}

func StartAnthropicOAuth() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	flow, err := auth.StartClaudeOAuthFlow(ctx)
	if err != nil {
		return "", fmt.Errorf("starting OAuth flow: %w", err)
	}

	oauthMu.Lock()
	if currentOAuthFlow != nil {
		currentOAuthFlow.Close()
	}
	currentOAuthFlow = flow

	oauthFlowEpoch++
	oauthMu.Unlock()
	return flow.AutoURL, nil
}

func CompleteAnthropicOAuth() (err error) {
	var call *oauthCompleteCall
	var flow *auth.ClaudeOAuthFlow
	for {

		oauthCompleteMu.Lock()
		if inflight := oauthCompleteInFlight; inflight != nil {

			oauthMu.Lock()
			flowEpoch := oauthFlowEpoch
			oauthMu.Unlock()
			if inflight.flowEpoch == flowEpoch {
				oauthCompleteMu.Unlock()
				<-inflight.done
				if inflight.err == nil {

					oauthMu.Lock()
					curLogoutGen := oauthLogoutGen
					oauthMu.Unlock()
					if inflight.logoutGen == curLogoutGen {
						return nil
					}
					return fmt.Errorf("oauth token saved by concurrent call was removed by a concurrent logout")
				}
				return fmt.Errorf("oauth completion by concurrent call failed: %w", inflight.err)
			}

			oauthCompleteMu.Unlock()
			<-inflight.done
			continue
		}
		oauthMu.Lock()
		call = &oauthCompleteCall{
			done:      make(chan struct{}),
			flowEpoch: oauthFlowEpoch,
			logoutGen: oauthLogoutGen,
		}

		flow = currentOAuthFlow
		oauthMu.Unlock()
		oauthCompleteInFlight = call
		oauthCompleteMu.Unlock()
		break
	}
	defer func() {

		oauthCompleteMu.Lock()
		call.err = err
		oauthCompleteInFlight = nil
		oauthCompleteMu.Unlock()
		close(call.done)
	}()

	if flow == nil {
		return fmt.Errorf("no OAuth flow in progress")
	}
	defer func() {
		oauthMu.Lock()
		flow.Close()

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

	if call.supersededByLogout() {
		debug.Log("wailskit", "oauth: token exchange finished after logout; discarding token instead of saving (#674)")
		return fmt.Errorf("logout occurred during token exchange; token not saved")
	}
	if err := auth.DefaultStore().Save(info); err != nil {
		return err
	}

	refreshRunningProviderAfterAuth()
	return nil
}

func refreshRunningProviderAfterAuth() {
	if bridge := GetChatBridge(); bridge != nil {
		bridge.OnConfigProviderChanged()
	}
}

func LogoutAnthropicOAuth() error {

	oauthMu.Lock()
	oauthLogoutGen++
	oauthFlowEpoch++
	if currentOAuthFlow != nil {
		currentOAuthFlow.Close()
		currentOAuthFlow = nil
	}
	oauthMu.Unlock()
	err := auth.DefaultStore().Delete(auth.ProviderAnthropic)

	refreshRunningProviderAfterAuth()
	return err
}

var (
	oauthMu          sync.Mutex
	currentOAuthFlow *auth.ClaudeOAuthFlow

	oauthFlowEpoch uint64

	oauthLogoutGen uint64

	oauthCompleteMu       sync.Mutex
	oauthCompleteInFlight *oauthCompleteCall
)

type oauthCompleteCall struct {
	done      chan struct{}
	err       error
	flowEpoch uint64
	logoutGen uint64
}

func (c *oauthCompleteCall) supersededByLogout() bool {
	oauthMu.Lock()
	defer oauthMu.Unlock()
	return c.logoutGen != oauthLogoutGen
}
