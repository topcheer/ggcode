package main

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

// ResolveProvider resolves the active endpoint, validates the API key,
// applies impersonation settings, and creates the provider.
// Shared by TUI, Pipe, and Daemon entry points.
func ResolveProvider(cfg *config.Config) (provider.Provider, *config.ResolvedEndpoint, error) {
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		return nil, nil, fmt.Errorf("resolving endpoint: %w", err)
	}
	if resolved.APIKey == "" {
		return nil, nil, fmt.Errorf("no API key for vendor %q endpoint %q. Set the api_key in config or /provider", resolved.VendorID, resolved.EndpointID)
	}

	// Apply impersonation settings from config before creating provider
	if imp := cfg.Impersonation; imp.Preset != "" {
		var preset *provider.ImpersonationPreset
		if imp.Preset != "none" {
			preset = provider.FindPresetByID(imp.Preset)
		}
		customHeaders := make(map[string]string, len(imp.CustomHeaders))
		for k, v := range imp.CustomHeaders {
			customHeaders[k] = v
		}
		provider.SetActiveImpersonation(preset, imp.CustomVersion, customHeaders)
	}

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return nil, nil, fmt.Errorf("creating provider: %w", err)
	}
	return prov, resolved, nil
}
