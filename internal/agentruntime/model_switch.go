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

	// Wrap in FallbackProvider if a fallback is configured and resolvable.
	// #1674: the guard checked ONLY the legacy single-entry field while
	// FallbackChain() (what wrapWithFallback consumes) merges BOTH the
	// legacy field and the modern fallbacks array - a pure `fallbacks:`
	// array config silently never got a failover wrapper at all.
	if cfg.Fallback.IsConfigured() || len(cfg.Fallbacks) > 0 {
		prov = wrapWithFallback(cfg, prov, resolved)
	}

	return resolved, prov, nil
}

func ActivateCurrentSelection(cfg *config.Config, vendor, endpoint, model string) (*config.ResolvedEndpoint, provider.Provider, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}
	// #1670 case 1: empty vendor/endpoint means "inherit the CURRENT
	// selection", not "look up vendor \"\"". The IM/daemon/desktop vision
	// turn switchers all pass ("", "", visionModel) - the original TUI
	// implementation passed the active cfg.Vendor/Endpoint explicitly, but
	// the ported callers dropped them, so every switch died on
	// `vendor "" is not configured`, the failure only hit the debug log,
	// and the image was silently stripped to a non-vision model (the
	// feature was dead on arrival for IM/desktop).
	if vendor == "" {
		vendor = cfg.Vendor
	}
	if endpoint == "" {
		endpoint = cfg.Endpoint
	}
	if vendor != "" || endpoint != "" || model != "" {
		// #1487: snapshot the active selection before SetActiveSelection
		// rewrites it, so a failed Resolve can roll back. Without this, a
		// switch to a model whose API key is missing (or a vendor returning
		// 401) left cfg pointing at the broken choice - later provider
		// rebuilds (ensureProviderSync, daemon restart, config reload) then
		// failed against it, taking down the previously working provider too.
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
	// sa-44 LLM cassette: wrap the provider per GGCODE_LLM_TAPE before it
	// reaches the agent. Both the daemon bootstrap paths and the config
	// hot-swap funnel through here, so this is the single choke point.
	prov = provider.WrapLLMTapeFromEnv(prov)
	agentInst.SetProvider(prov)
	ApplyResolvedLimitsToAgent(agentInst, resolved)
	agentInst.SetSupportsVision(resolved.SupportsVision)
	agentInst.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))
	// #2511: the agent-side memory executor answers name="memory" calls only
	// when the provider actually declares the tool (anthropic protocol +
	// endpoint `memory_tool: true`, mirroring the provider registry's
	// SetMemoryTool gate). Any other provider keeps the executor disabled so
	// hallucinated "memory" calls fall through to UnknownToolError.
	agentInst.SetMemoryToolEnabled(resolved.Protocol == "anthropic" && resolved.MemoryTool)

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
	// Always propagate, including 0 (#1494): a config reload that removes
	// session_token_budget must reset any previously applied explicit
	// budget, otherwise the old cap survives until restart - the same
	// always-call semantics ApplyToolCallBudget adopted for the sibling
	// #543 bug (SetSessionTokenBudget(0) clears the explicit budget).
	agentInst.SetSessionTokenBudget(cfg.SessionTokenBudget)
}

// ApplySessionCostLimit propagates the configured per-run USD spend cap
// to the agent, resolving the active model's per-million rates from the
// cost pricing table (sa-56). Call after agent creation, config reload,
// or model switch — rates are per-model so a switch must re-apply.
// Subscription/bundled/free and unknown-pricing models yield zero rates:
// the limit is a no-op for them and session_token_budget remains the
// effective ceiling (documented in session_cost_limit.go).
func ApplySessionCostLimit(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}
	limit := cfg.SessionCostLimitUSD
	var in, out, cr, cw float64
	if limit > 0 {
		// Same resolution order as the TUI /cost breakdown
		// (commands_slash_info.go resolveRate) so both agree on the bill.
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

// ApplySessionTimeBudget propagates the configured session-level wall-clock
// soft budget to the agent (r415 time-dimension ladder). Call this after
// agent creation or config reload.
func ApplySessionTimeBudget(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}
	// Always propagate, including 0 — same always-call semantics as
	// ApplySessionTokenBudget (#1494): a reload that removes
	// session_time_budget must reset any previously applied budget.
	agentInst.SetSessionTimeBudget(cfg.SessionTimeBudget)
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
// #2911: this runs from model-switch hooks (cmd daemon SetProviderSwitchHook,
// desktop ChatBridge.SwitchModel) that hold no configAccess instance and thus
// cannot take cfgMu. Mutating cfg.Vendors directly here raced the cfgMu-guarded
// writers (configAccess Set*APIKey, hot-reload applyFreshConfig) into a Go
// runtime-fatal concurrent map write. The map mutation now goes through
// Config.UpsertVendorEndpoint, which serializes all runtime Vendors writers
// under config.vendorsWriteMu. SaveScoped stays OUTSIDE that lock (disk I/O,
// #957 lesson: no file writes under locks that hot paths also need).
func SyncVendorEndpointToGlobal(cfg *config.Config, vendor, endpoint string) {
	if cfg == nil || vendor == "" || endpoint == "" {
		return
	}
	if cfg.UpsertVendorEndpoint(vendor, endpoint) {
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
