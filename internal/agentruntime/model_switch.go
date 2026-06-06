package agentruntime

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func ResolveCurrentSelection(cfg *config.Config) (*config.ResolvedEndpoint, provider.Provider, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		return nil, nil, err
	}
	if resolved.APIKey == "" {
		if resolved.AuthType == "oauth" {
			return nil, nil, fmt.Errorf("no login configured for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
		}
		return nil, nil, fmt.Errorf("no api key configured for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return nil, nil, err
	}
	return resolved, prov, nil
}

func ActivateCurrentSelection(cfg *config.Config, vendor, endpoint, model string) (*config.ResolvedEndpoint, provider.Provider, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}
	if vendor != "" || endpoint != "" || model != "" {
		if err := cfg.SetActiveSelection(vendor, endpoint, model); err != nil {
			return nil, nil, err
		}
		if err := cfg.Save(); err != nil {
			return nil, nil, err
		}
	}
	return ResolveCurrentSelection(cfg)
}

func ApplyProviderToAgent(agentInst *agent.Agent, prov provider.Provider, resolved *config.ResolvedEndpoint) {
	if agentInst == nil || prov == nil || resolved == nil {
		return
	}
	agentInst.SetProvider(prov)
	ApplyResolvedLimitsToAgent(agentInst, resolved)
	agentInst.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))
}
