package main

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func ResolveProvider(cfg *config.Config) (provider.Provider, *config.ResolvedEndpoint, error) {

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

	resolved, prov, err := agentruntime.ResolveCurrentSelection(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving provider selection: %w", err)
	}
	return prov, resolved, nil
}
