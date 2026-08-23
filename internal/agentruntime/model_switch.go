package agentruntime

import (
	"fmt"
	"strings"

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

// ApplyToolCallBudget propagates the configured tool call budget to the agent.
// Call this after agent creation or config reload. When unset (0), the agent
// auto-derives a default from maxIterations.
func ApplyToolCallBudget(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}
	// Always propagate, including 0 (#543): a config reload that removes
	// tool_call_budget must reset any previously applied explicit budget,
	// otherwise the old value survives until restart. 0 clears the explicit
	// budget and lets auto-derivation from maxIter apply — the same
	// always-call semantics as ApplySessionTimeout.
	agentInst.SetToolCallBudget(cfg.ToolCallBudget)
}

// ApplySessionTimeout propagates the configured wall-clock session timeout to
// the agent. In autopilot mode, a default timeout is applied when unset.
func ApplySessionTimeout(agentInst *agent.Agent, cfg *config.Config, isAutopilot bool) {
	if agentInst == nil || cfg == nil {
		return
	}
	timeout := agent.EffectiveSessionTimeout(cfg.SessionTimeout, isAutopilot)
	agentInst.SetSessionTimeout(timeout)
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

// wrapWithFallback creates a failover wrapper around the primary provider.
// Supports a priority-ordered chain: the legacy single `fallback` entry
// first, then each `fallbacks` list entry in order (earlier = higher
// priority). If an entry cannot be resolved it is skipped - failover is
// best-effort, never blocking. With no usable fallback entries the primary
// is returned unwrapped.
func wrapWithFallback(cfg *config.Config, primary provider.Provider, primaryResolved *config.ResolvedEndpoint) provider.Provider {
	var fallbacks []provider.Provider
	var descs []string

	appendEntry := func(fb config.FallbackConfig) {
		fbResolved, err := cfg.ResolveEndpoint(fb.Vendor, fb.Endpoint)
		if err != nil {
			debug.Log("provider", "fallback skipped: cannot resolve %s/%s: %v", fb.Vendor, fb.Endpoint, err)
			return
		}
		if fbResolved.APIKey == "" {
			debug.Log("provider", "fallback skipped: no API key for %s/%s", fb.Vendor, fb.Endpoint)
			return
		}
		// Override model from fallback config.
		fbResolved.Model = fb.Model
		fbProv, err := provider.NewProvider(fbResolved)
		if err != nil {
			debug.Log("provider", "fallback skipped: cannot create provider: %v", err)
			return
		}
		fallbacks = append(fallbacks, fbProv)
		descs = append(descs, fmt.Sprintf("%s/%s/%s", fbResolved.VendorID, fbResolved.EndpointID, fbResolved.Model))
	}

	for _, fb := range cfg.FallbackChain() {
		appendEntry(fb)
	}
	if len(fallbacks) == 0 {
		return primary
	}

	chain := append([]provider.Provider{primary}, fallbacks...)
	primaryDesc := fmt.Sprintf("%s/%s/%s", primaryResolved.VendorID, primaryResolved.EndpointID, primaryResolved.Model)
	desc := strings.Join(append([]string{primaryDesc}, descs...), " -> ")
	debug.Log("provider", "fallback chain enabled: %s", desc)
	return provider.NewCascadeProvider(chain, desc)
}
