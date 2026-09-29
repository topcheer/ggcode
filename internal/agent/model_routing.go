package agent

// model_routing.go -- Task-tier model routing for auxiliary LLM calls
// (LiteLLM-style routing, 2026 "model routing" practice).
//
// Research basis:
//   - "LLM Model Routing in 2026" engineering guides: route each request to
//     the cheapest model that can handle it; routing mechanical/auxiliary
//     calls (summarization, classification, guidance) to a small model cuts
//     LLM cost 40-85% with no visible quality loss.
//   - Anthropic "context engineering" guidance: an agent harness makes many
//     non-primary LLM calls (compaction summarization, autopilot strategist,
//     health probes). These are bounded, mechanical tasks that do not need
//     the frontier reasoning model.
//
// Gap in this codebase: every auxiliary LLM call (autopilot strategist,
// health check, compaction summarization) goes through the SAME provider and
// model as the primary conversation loop, so a user paying for a frontier
// reasoning model also pays frontier prices for mechanical summarization.
// A failure fallback chain (config `fallbacks`) switches models on FAILURE
// only; it cannot route by task tier.
//
// Design (fully opt-in, zero behavior change when unset):
//   - config `aux_model: <name>` names a cheaper model on the SAME resolved
//     endpoint (same vendor / API key / protocol) for auxiliary calls.
//   - SetAuxModel wires the resolved endpoint + aux model into the Agent.
//   - auxProviderFor() lazily builds a second provider by cloning the
//     resolved endpoint with Model=aux_model and caching it. On construction
//     failure it logs once and permanently falls back to the main provider
//     (aux routing must never break the primary loop).
//   - auxProviderFor() is the single routing decision point; call sites pass
//     its result wherever a provider.Provider is consumed for auxiliary work
//     (strategist, health check, CheckAndSummarize). The primary
//     conversation loop always uses a.provider.
//
// Protocol safety: aux calls reuse the provider Chat API - no message-shape
// changes, nothing inserted between tool_calls and tool_results.

import (
	"context"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// SetAuxModel enables task-tier routing of auxiliary LLM calls to `model`
// on the same resolved endpoint. Empty model disables routing (default).
func (a *Agent) SetAuxModel(resolved *config.ResolvedEndpoint, model string) {
	if resolved == nil || model == "" || model == resolved.Model {
		return // disabled: aux model unset or identical to the main model
	}
	clone := *resolved
	clone.Model = model
	a.auxResolved = &clone
	a.auxModelName = model
}

// auxProviderFor returns the provider to use for auxiliary (non-primary)
// LLM calls: the cached aux provider when routing is enabled, the main
// provider otherwise. Construction failures degrade permanently to the main
// provider - aux routing is best-effort by design.
func (a *Agent) auxProviderFor() provider.Provider {
	if a.auxResolved == nil {
		return a.provider
	}
	if a.auxFailed {
		return a.provider
	}
	if a.auxProvider != nil {
		return a.auxProvider
	}
	p, err := provider.NewProvider(a.auxResolved)
	if err != nil {
		// Log once; permanently degrade to the main provider so a bad aux
		// model name never breaks compaction/strategist availability.
		debug.Log("agent", "model routing: aux provider build failed for %q, falling back to main model: %v",
			a.auxModelName, err)
		a.auxFailed = true
		return a.provider
	}
	a.auxProvider = p
	debug.Log("agent", "model routing: auxiliary calls routed to %q (main: %q)",
		a.auxModelName, a.auxResolvedModelName())
	return p
}

// auxResolvedModelName returns the main model name for diagnostics; the
// aux clone carries the aux model in .Model, so this re-derives the original
// from the provider's own state is unnecessary - we snapshot the main name
// at SetAuxModel time via the clone's non-aux sibling. Kept simple: report
// the aux model only.
func (a *Agent) auxResolvedModelName() string { return a.auxModelName }

// auxChat performs a non-streaming auxiliary LLM call on the routing tier.
// Signature mirrors provider.Chat so call sites are drop-in replacements.
func (a *Agent) auxChat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return a.auxProviderFor().Chat(ctx, messages, tools)
}
