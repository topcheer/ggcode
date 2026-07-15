package agentruntime

import (
	"context"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/relaycatalog"
	"github.com/topcheer/ggcode/internal/safego"
)

func ApplyResolvedLimitsToAgent(agentInst *agent.Agent, resolved *config.ResolvedEndpoint) {
	if agentInst == nil || resolved == nil {
		return
	}
	if resolved.ContextWindow > 0 {
		agentInst.ContextManager().SetContextWindow(resolved.ContextWindow)
	}
	if resolved.MaxTokens > 0 {
		agentInst.ContextManager().SetOutputReserve(resolved.MaxTokens)
	}
}

// StartAsyncRelayModelLimitRefresh fetches the real context_window / max_output_tokens
// from the relay catalog and applies them to the agent. sessionCW and sessionMT are
// session-level overrides (0 = not set); when non-zero they are re-applied AFTER the
// relay catalog values so that user-edited limits from the model panel are not
// clobbered by the async background refresh.
func StartAsyncRelayModelLimitRefresh(cfg *config.Config, resolved *config.ResolvedEndpoint, agentInst *agent.Agent, onApplied func(relaycatalog.ResolveResponse)) {
	startAsyncRelayModelLimitRefreshWithSession(cfg, resolved, agentInst, 0, 0, onApplied)
}

// StartAsyncRelayModelLimitRefreshWithSession is like StartAsyncRelayModelLimitRefresh
// but re-applies session-level overrides after the relay catalog values are set.
func StartAsyncRelayModelLimitRefreshWithSession(cfg *config.Config, resolved *config.ResolvedEndpoint, agentInst *agent.Agent, sessionCW, sessionMT int, onApplied func(relaycatalog.ResolveResponse)) {
	startAsyncRelayModelLimitRefreshWithSession(cfg, resolved, agentInst, sessionCW, sessionMT, onApplied)
}

func startAsyncRelayModelLimitRefreshWithSession(cfg *config.Config, resolved *config.ResolvedEndpoint, agentInst *agent.Agent, sessionCW, sessionMT int, onApplied func(relaycatalog.ResolveResponse)) {
	if cfg == nil || resolved == nil || agentInst == nil {
		return
	}
	ep := cfg.ActiveEndpointConfig()
	allowContextOverride := ep == nil || ep.ContextWindow <= 0
	allowMaxTokenOverride := ep == nil || ep.MaxTokens <= 0
	if !allowContextOverride && !allowMaxTokenOverride && sessionCW <= 0 && sessionMT <= 0 {
		return
	}
	expectedVendor := strings.TrimSpace(resolved.VendorID)
	expectedEndpoint := strings.TrimSpace(resolved.EndpointID)
	expectedBaseURL := strings.TrimSpace(resolved.BaseURL)
	expectedModel := strings.TrimSpace(resolved.Model)
	if expectedVendor == "" || expectedModel == "" {
		return
	}

	safego.Go("agentruntime.relayCatalogResolve", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		resp, err := relaycatalog.Resolve(ctx, relaycatalog.RelayURL(), expectedVendor, expectedModel)
		if err != nil {
			debug.Log("relay-catalog", "resolve failed for %s/%s: %v", expectedVendor, expectedModel, err)
			return
		}
		if !resp.Found {
			debug.Log("relay-catalog", "no match for %s/%s", expectedVendor, expectedModel)
			return
		}
		current, err := cfg.ResolveActiveEndpoint()
		if err != nil {
			debug.Log("relay-catalog", "skip apply: active resolve failed: %v", err)
			return
		}
		if strings.TrimSpace(current.VendorID) != expectedVendor ||
			strings.TrimSpace(current.EndpointID) != expectedEndpoint ||
			strings.TrimSpace(current.BaseURL) != expectedBaseURL ||
			strings.TrimSpace(current.Model) != expectedModel {
			debug.Log("relay-catalog", "skip stale apply for %s/%s", expectedVendor, expectedModel)
			return
		}

		applied := false
		if allowContextOverride && resp.ContextWindow > 0 {
			agentInst.ContextManager().SetContextWindow(resp.ContextWindow)
			applied = true
		}
		if allowMaxTokenOverride && resp.MaxOutputTokens > 0 {
			agentInst.ContextManager().SetOutputReserve(resp.MaxOutputTokens)
			applied = true
		}
		// Re-apply session-level overrides so user-edited values from the
		// model panel are not clobbered by relay catalog defaults.
		if sessionCW > 0 {
			agentInst.ContextManager().SetContextWindow(sessionCW)
			applied = true
		}
		if sessionMT > 0 {
			agentInst.ContextManager().SetOutputReserve(sessionMT)
			applied = true
		}
		if applied && onApplied != nil {
			onApplied(resp)
		}
	})
}
