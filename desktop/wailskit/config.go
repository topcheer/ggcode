// Package wailskit provides a public facade for the Wails desktop app
// to access internal config and other services without violating Go's
// internal package rules.
package wailskit

import (
	"sort"

	"github.com/topcheer/ggcode/internal/config"
)

// ConfigSnapshot is a read-only snapshot of the config for frontend use.
type ConfigSnapshot struct {
	Vendor      string `json:"vendor"`
	Endpoint    string `json:"endpoint"`
	Model       string `json:"model"`
	DefaultMode string `json:"defaultMode"`
	Language    string `json:"language"`
	ExtraPrompt string `json:"extraPrompt"`
}

// GetConfig loads and returns the current config.
func GetConfig() (*ConfigSnapshot, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, err
	}
	return &ConfigSnapshot{
		Vendor:      cfg.Vendor,
		Endpoint:    cfg.Endpoint,
		Model:       cfg.Model,
		DefaultMode: cfg.DefaultMode,
		Language:    cfg.Language,
		ExtraPrompt: cfg.ExtraPrompt,
	}, nil
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

// SaveConfig applies values to the config and saves.
func SaveConfig(values map[string]string) error {
	cfg, err := config.Load("")
	if err != nil {
		cfg = config.DefaultConfig()
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
