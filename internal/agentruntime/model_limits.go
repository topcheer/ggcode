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

func StartAsyncRelayModelLimitRefresh(cfg *config.Config, resolved *config.ResolvedEndpoint, agentInst *agent.Agent, onApplied func(relaycatalog.ResolveResponse)) {
	if cfg == nil || resolved == nil || agentInst == nil {
		return
	}
	ep := cfg.ActiveEndpointConfig()
	allowContextOverride := ep == nil || ep.ContextWindow <= 0
	allowMaxTokenOverride := ep == nil || ep.MaxTokens <= 0
	if !allowContextOverride && !allowMaxTokenOverride {
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
		if applied && onApplied != nil {
			onApplied(resp)
		}
	})
}
