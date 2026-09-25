package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Utility workload model routing ("compound AI" per-workflow model binding).
//
// Auxiliary, non-conversational LLM workloads do not need a frontier model:
// the autopilot strategist's next-action reasoning pass and the reactive
// compaction summarization are bounded, self-contained tasks that a smaller
// or cheaper model handles fine. Routing them to a separate model cuts cost
// and latency without touching the conversational quality of the main loop.
//
// Resolution order for the utility model name:
//  1. cfg.UtilityModel (`utility_model:` in config) - explicit user choice
//  2. the active vendor's built-in small-model default (vendorDefaultModels
//     SmallModel; empty for every built-in vendor today, so this is purely a
//     future-facing hook and changes nothing by default)
//  3. "" - no routing; utility work stays on the primary provider (the
//     historical behavior)
//
// The routed provider only differs from the primary in its model name: it
// reuses the active vendor/endpoint credentials, protocol adapter, and HTTP
// settings. Failure to resolve is always best-effort and non-blocking.

// UtilityModelName resolves the model auxiliary ("utility") LLM workloads
// should run on. Returns "" when utility work should stay on the primary
// model. Never panics on nil config.
func UtilityModelName(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if m := strings.TrimSpace(cfg.UtilityModel); m != "" {
		return m
	}
	return config.VendorSmallModelDefault(cfg.Vendor)
}

// ResolveUtilityProvider builds the provider for utility workloads, or
// returns (nil, nil) when no routing applies: no utility model configured,
// the endpoint has no credentials, or the utility model equals the primary
// model (a second identical provider would only add confusion). Resolution
// errors are returned so the caller can log them; ApplyUtilityProvider
// treats every error as "keep utility work on the primary provider".
func ResolveUtilityProvider(cfg *config.Config) (provider.Provider, error) {
	name := UtilityModelName(cfg)
	if name == "" {
		return nil, nil
	}
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		return nil, err
	}
	if resolved.APIKey == "" {
		debug.Log("provider", "utility routing skipped: no api key for %s/%s", resolved.VendorID, resolved.EndpointID)
		return nil, nil
	}
	if name == resolved.Model {
		return nil, nil
	}
	u := *resolved
	u.Model = name
	prov, err := provider.NewProvider(&u)
	if err != nil {
		return nil, err
	}
	debug.Log("provider", "utility model routing enabled: %s/%s %s -> %s", resolved.VendorID, resolved.EndpointID, resolved.Model, name)
	return prov, nil
}

// ApplyUtilityProvider resolves the utility model and installs it on the
// agent for auxiliary workloads (autopilot strategist passes, reactive
// compaction summarization). Best-effort by design: any failure logs and
// leaves the agent routing utility work to its primary provider. Call after
// agent creation and on every provider hot-swap - utility routing derives
// from the active vendor/endpoint, which switches may change. A nil utility
// provider explicitly resets routing to the primary provider.
func ApplyUtilityProvider(agentInst *agent.Agent, cfg *config.Config) {
	if agentInst == nil || cfg == nil {
		return
	}
	prov, err := ResolveUtilityProvider(cfg)
	if err != nil {
		debug.Log("provider", "utility provider resolution failed (utility work stays on primary): %v", err)
		prov = nil
	}
	agentInst.SetUtilityProvider(prov)
}
