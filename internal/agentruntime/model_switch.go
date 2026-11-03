package agentruntime

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
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

	if cfg.Fallback.IsConfigured() || len(cfg.Fallbacks) > 0 {
		prov = wrapWithFallback(cfg, prov, resolved)
	}

	return resolved, prov, nil
}

func ActivateCurrentSelection(cfg *config.Config, vendor, endpoint, model string) (*config.ResolvedEndpoint, provider.Provider, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}

	if vendor == "" {
		vendor = cfg.Vendor
	}
	if endpoint == "" {
		endpoint = cfg.Endpoint
	}
	if vendor != "" || endpoint != "" || model != "" {

		oldVendor, oldEndpoint, oldModel := cfg.Vendor, cfg.Endpoint, cfg.Model
		var oldSel string
		if vc, ok := cfg.Vendors[vendor]; ok {
			if ep, ok2 := vc.Endpoints[endpoint]; ok2 {
				oldSel = ep.SelectedModel
			}
		}
		if err := cfg.SetActiveSelection(vendor, endpoint, model); err != nil {
			return nil, nil, err
		}
		resolved, prov, rerr := ResolveCurrentSelection(cfg)
		if rerr != nil {
			cfg.Vendor, cfg.Endpoint, cfg.Model = oldVendor, oldEndpoint, oldModel
			if vc, ok := cfg.Vendors[vendor]; ok {
				if ep, ok2 := vc.Endpoints[endpoint]; ok2 {
					ep.SelectedModel = oldSel
					vc.Endpoints[endpoint] = ep
					cfg.Vendors[vendor] = vc
				}
			}
			return nil, nil, rerr
		}
		return resolved, prov, nil

	}
	return ResolveCurrentSelection(cfg)
}

func ApplyProviderToAgent(agentInst *agent.Agent, prov provider.Provider, resolved *config.ResolvedEndpoint) {
	if agentInst == nil || prov == nil || resolved == nil {
		return
	}

	if _, alreadyWrapped := prov.(*provider.TapeProvider); !alreadyWrapped {
		prov = provider.WrapLLMTapeFromEnv(prov)
	}

	if _, alreadyFaulted := prov.(*provider.FaultProvider); !alreadyFaulted {
		prov = provider.WrapLLMFaultFromEnv(prov)
	}
	agentInst.SetProvider(prov)
	ApplyResolvedLimitsToAgent(agentInst, resolved)
	agentInst.SetSupportsVision(resolved.SupportsVision)
	agentInst.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))

	agentInst.SetMemoryToolEnabled(resolved.Protocol == "anthropic" && resolved.MemoryTool)

	if ss, ok := prov.(provider.SessionIDSetter); ok {
		ss.SetSessionID(agentInst.SessionID())
	}
}

func ApplySessionTokenBudget(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}

	agentInst.SetSessionTokenBudget(cfg.SessionTokenBudget)
}

func ApplySessionCostLimit(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}
	limit := cfg.SessionCostLimitUSD
	var in, out, cr, cw float64
	if limit > 0 {

		pt := cost.DefaultPricingTable()
		var rate cost.ModelRate
		if r, ok := pt.Get(cfg.Vendor, cfg.Model); ok {
			rate = r
		} else if cost.IsCodingPlanEndpoint(cfg.Endpoint) {
			rate = cost.ModelRate{Type: cost.PricingSubscription, Plan: "Coding Plan"}
		} else if plan := cost.IsSubscriptionVendor(cfg.Vendor); plan != "" {
			rate = cost.ModelRate{Type: cost.PricingSubscription, Plan: plan}
		}
		if rate.IsMetered() {
			in, out, cr, cw = rate.InputPerM, rate.OutputPerM, rate.CacheReadPerM, rate.CacheWritePerM
		} else {
			debug.Log("session-cost-limit", "limit set but model %s/%s is %s — accumulating $0", cfg.Vendor, cfg.Model, rate.Type)
		}
	}
	agentInst.SetSessionCostLimit(limit, in, out, cr, cw)
}

func ApplySessionTimeBudget(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}

	agentInst.SetSessionTimeBudget(cfg.SessionTimeBudget)
}

func ApplyToolCallBudget(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}

	agentInst.SetToolCallBudget(cfg.ToolCallBudget)
}

func ApplySessionTimeout(agentInst *agent.Agent, cfg *config.Config, isAutopilot bool) {
	if agentInst == nil || cfg == nil {
		return
	}
	timeout := agent.EffectiveSessionTimeout(cfg.SessionTimeout, isAutopilot)
	agentInst.SetSessionTimeout(timeout)
}

func SyncVendorEndpointToGlobal(cfg *config.Config, vendor, endpoint string) {
	if cfg == nil || vendor == "" || endpoint == "" {
		return
	}
	if cfg.UpsertVendorEndpoint(vendor, endpoint) {
		_ = cfg.SaveScoped("global")
	}
}

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
