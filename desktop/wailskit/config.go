// Package wailskit provides a public facade for the Wails desktop app
// to access internal config and other services without violating Go's
// internal package rules.
package wailskit

import (
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
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
// Mirrors desktop/ggcode-desktop/app.go resolveConfigFilePath.
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
// Uses LoadWithInstance like the Fyne desktop does.
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

// ConfigSnapshot is a read-only snapshot of the config for frontend use.
type ConfigSnapshot struct {
	Vendor      string `json:"vendor"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	DefaultMode string `json:"defaultMode"`
	Language    string `json:"language"`
	ExtraPrompt string `json:"extraPrompt"`
	NeedsSetup  bool   `json:"needsSetup"`
}

// GetConfig returns the current config snapshot.
func GetConfig() (*ConfigSnapshot, error) {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return &ConfigSnapshot{NeedsSetup: true}, nil
	}
	return &ConfigSnapshot{
		Vendor:      cfg.Vendor,
		Endpoint:    cfg.Endpoint,
		Model:       cfg.Model,
		DefaultMode: cfg.DefaultMode,
		Language:    cfg.Language,
		ExtraPrompt: cfg.ExtraPrompt,
		NeedsSetup:  cfg.NeedsOnboard(),
	}, nil
}

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

// VendorNames returns available vendor names from the default config.
func VendorNames() []string {
	cfg := config.DefaultConfig()
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
	cfg := config.DefaultConfig()
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

// ModelsForEndpoint returns available model names for a given vendor and endpoint key.
func ModelsForEndpoint(vendor, endpointKey string) []string {
	cfg := config.DefaultConfig()
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

// SaveConfig applies values to the global config and saves.
func SaveConfig(values map[string]string) error {
	globalMu.RLock()
	cfg := globalCfg
	globalMu.RUnlock()
	if cfg == nil {
		return nil
	}
	if v, ok := values["vendor"]; ok {
		cfg.Vendor = v
	}
	if v, ok := values["endpoint"]; ok {
		cfg.Endpoint = v
	}
	if v, ok := values["model"]; ok {
		cfg.Model = v
	}
	if v, ok := values["defaultMode"]; ok {
		cfg.DefaultMode = v
	}
	if v, ok := values["language"]; ok {
		cfg.Language = v
	}
	if v, ok := values["extraPrompt"]; ok {
		cfg.ExtraPrompt = v
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
	cfg.SetEndpointAPIKey(vendor, endpoint, apiKey, true)
	return cfg.Save()
}
