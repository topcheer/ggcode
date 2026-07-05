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
		// NOTE: cfg.Save() was intentionally removed.
		// Model selection is now session-scoped — the session JSONL is the
		// source of truth, not the config file. Callers are responsible for
		// persisting the session after updating its Vendor/Endpoint/Model.
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

// SyncVendorEndpointToGlobal ensures a vendor/endpoint definition exists in
// the global config file so new sessions can discover it without re-configuring
// API keys. This is called after model switches to propagate vendor/endpoint
// definitions that were added during the current session.
func SyncVendorEndpointToGlobal(cfg *config.Config, vendor, endpoint string) {
	if cfg == nil || vendor == "" || endpoint == "" {
		return
	}
	changed := false
	if cfg.Vendors == nil {
		cfg.Vendors = make(map[string]config.VendorConfig)
	}
	vc, ok := cfg.Vendors[vendor]
	if !ok {
		vc = config.VendorConfig{Endpoints: make(map[string]config.EndpointConfig)}
		cfg.Vendors[vendor] = vc
		changed = true
	}
	if _, ok := vc.Endpoints[endpoint]; !ok {
		vc.Endpoints[endpoint] = config.EndpointConfig{}
		cfg.Vendors[vendor] = vc
		changed = true
	}
	if changed {
		_ = cfg.SaveScoped("global")
	}
}
