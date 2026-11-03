package wailskit

import (
	"context"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/usage"
)

type UsageWindowInfo struct {
	Label       string  `json:"label"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    string  `json:"resetsAt"`
}

type UsageInfoResult struct {
	Vendor  string            `json:"vendor"`
	Source  string            `json:"source"`
	Balance *float64          `json:"balance"`
	Windows []UsageWindowInfo `json:"windows"`
	Error   string            `json:"error"`
}

var usageService = usage.DefaultService()

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

	probeID := usageService.Resolve(ep.baseURL)
	if probeID == "" {
		res.Error = "no usage probe for this endpoint"
		return res
	}

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
