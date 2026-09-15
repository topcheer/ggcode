package wailskit

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/usage"
)

// #2150 batch 3 (first slice): desktop-side consumption of the usage layer
// landed in #2352. The ChatBridge exposes GetUsageInfo as a Wails binding -
// the desktop UI polls it for the usage badge / panel. The IM /usage render
// and the badge visuals are follow-up slices; this binding is the data door.

// UsageWindowInfo is the JSON shape of one rolling window for the frontend.
type UsageWindowInfo struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    string  `json:"resetsAt"` // RFC3339, "" when unknown
}

// UsageInfoResult is the JSON shape returned to the desktop frontend.
type UsageInfoResult struct {
	Vendor  string            `json:"vendor"`
	Source  string            `json:"source"`
	Balance *float64          `json:"balance"` // null when the vendor has no PAYG balance
	Windows []UsageWindowInfo `json:"windows"`
	Error   string            `json:"error"` // non-empty when the probe failed (shown as a muted hint)
}

// usageService is the process-wide cache (3min TTL + negative cache +
// singleflight from #2352); desktop polling and any later consumer share it.
// #2150 batch 2b: DefaultService is the single registration site for all
// P1 probes - consumers no longer inline their own (three-list drift made
// kimi/minimax/anthropic-oauth/openrouter/siliconflow invisible here and
// in the TUI until both were updated in lockstep).
var usageService = usage.DefaultService()

// GetUsageInfo resolves the ACTIVE vendor from the global config and returns
// its live usage/balance through the cached probe layer. Meant to be polled
// by the desktop UI; errors degrade to a result with Error set (never a Wails
// error popup - usage is ambient information, not a blocking action).
func (b *ChatBridge) GetUsageInfo() UsageInfoResult {
	res := UsageInfoResult{Windows: []UsageWindowInfo{}}
	cfg := GetGlobalConfig()
	if cfg == nil || cfg.Vendor == "" {
		res.Error = "no active vendor"
		return res
	}
	res.Vendor = cfg.Vendor

	ep := usageEndpointFor(cfg)
	if ep.apiKey == "" {
		res.Error = "no API key resolved for the active endpoint"
		return res
	}

	// #2399: probe selection is BY URL (owner ruling 2026-09-15) - resolve
	// the probe id from the ACTIVE endpoint's base URL, never the config
	// vendor name; unknown hosts mean no probe (ambient, stays silent).
	probeID := usageService.Resolve(ep.baseURL)
	if probeID == "" {
		res.Error = "no usage probe for this endpoint"
		return res
	}
	// #2400: the outer ctx must not clamp the probe's own 10s budget -
	// 5s outer x 10s inner made 5-10s endpoints fail deterministically
	// with DeadlineExceeded AND poisoned the negative cache. Aligned with
	// the TUI baseline (15s outer).
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := usageService.Get(ctx, probeID, ep.baseURL, ep.apiKey)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if info == nil {
		res.Error = "no usage data"
		return res
	}
	res.Source = info.Source
	res.Balance = info.Balance
	for _, w := range info.Windows {
		row := UsageWindowInfo{Label: w.Label, UsedPercent: w.UsedPercent}
		if !w.ResetsAt.IsZero() {
			row.ResetsAt = w.ResetsAt.Format(time.RFC3339)
		}
		res.Windows = append(res.Windows, row)
	}
	return res
}

type usageEndpoint struct{ baseURL, apiKey string }

// usageEndpointFor resolves the active endpoint's base URL and API key from
// the loaded config. #2284-A strips config-managed secrets from child
// shells, but the in-process config retains the resolved key (keys.env
// seeding happens inside config.Load).
func usageEndpointFor(cfg *config.Config) usageEndpoint {
	v, ok := cfg.Vendors[cfg.Vendor]
	if !ok {
		return usageEndpoint{}
	}
	ep, ok := v.Endpoints[cfg.Endpoint]
	if !ok {
		return usageEndpoint{}
	}
	return usageEndpoint{baseURL: ep.BaseURL, apiKey: ep.APIKey}
}
