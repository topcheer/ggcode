package agentruntime

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
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

	// Wrap in FallbackProvider if a fallback is configured and resolvable.
	if cfg.Fallback.IsConfigured() {
		prov = wrapWithFallback(cfg, prov, resolved)
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
	agentInst.SetSupportsVision(resolved.SupportsVision)
	agentInst.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))

	// Inject session ID into provider HTTP headers.
	if ss, ok := prov.(provider.SessionIDSetter); ok {
		ss.SetSessionID(agentInst.SessionID())
	}
}

// ApplySessionTokenBudget propagates the configured session-level token
// budget to the agent. Call this after agent creation or config reload.
func ApplySessionTokenBudget(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}
	if cfg.SessionTokenBudget > 0 {
		agentInst.SetSessionTokenBudget(cfg.SessionTokenBudget)
	}
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

// wrapWithFallback creates a FallbackProvider wrapping the primary provider.
// If the fallback endpoint cannot be resolved (missing vendor/endpoint/API key),
// the primary is returned unwrapped — failover is best-effort, never blocking.
func wrapWithFallback(cfg *config.Config, primary provider.Provider, primaryResolved *config.ResolvedEndpoint) provider.Provider {
	fbResolved, err := cfg.ResolveEndpoint(cfg.Fallback.Vendor, cfg.Fallback.Endpoint)
	if err != nil {
		debug.Log("provider", "fallback disabled: cannot resolve %s/%s: %v", cfg.Fallback.Vendor, cfg.Fallback.Endpoint, err)
		return primary
	}
	if fbResolved.APIKey == "" {
		debug.Log("provider", "fallback disabled: no API key for %s/%s", cfg.Fallback.Vendor, cfg.Fallback.Endpoint)
		return primary
	}
	// Override model from fallback config.
	fbResolved.Model = cfg.Fallback.Model
	fbProv, err := provider.NewProvider(fbResolved)
	if err != nil {
		debug.Log("provider", "fallback disabled: cannot create provider: %v", err)
		return primary
	}
	desc := fmt.Sprintf("%s/%s/%s -> %s/%s/%s",
		primaryResolved.VendorID, primaryResolved.EndpointID, primaryResolved.Model,
		fbResolved.VendorID, fbResolved.EndpointID, fbResolved.Model)
	debug.Log("provider", "fallback enabled: %s", desc)
	return provider.NewFallbackProvider(primary, fbProv, desc)
}
