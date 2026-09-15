package usage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// KimiProbe reads /v1/usages: the limits array carries rolling windows
// (detail names them: "5h"), the usage object carries the weekly quota
// usage. sub2api's parseKimiUsageTiers shape.
type KimiProbe struct{}

func (KimiProbe) Vendor() string { return "kimi" }

// MatchesURL: kimi shares moonshot hosts (cn platform account view).
func (KimiProbe) MatchesURL(host string) bool { return hostMatch(host, "api.moonshot.cn") }

func (KimiProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	var payload struct {
		Limits []struct {
			Detail      string  `json:"detail"`
			UsedPercent float64 `json:"used_percent"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"limits"`
		Usage struct {
			UsedPercent float64 `json:"used_percent"`
		} `json:"usage"`
	}
	if err := getJSON(ctx, base+"/v1/usages", apiKey, &payload); err != nil {
		return nil, err
	}
	info := &UsageInfo{Vendor: "kimi", Source: "v1/usages"}
	for _, l := range payload.Limits {
		label := strings.TrimSpace(l.Detail)
		if label == "" {
			continue
		}
		w := UsageWindow{Label: label, UsedPercent: l.UsedPercent}
		w.ResetsAt = parseResetTime(l.ResetsAt)
		info.Windows = append(info.Windows, w)
	}
	if payload.Usage.UsedPercent > 0 {
		info.Windows = append(info.Windows, UsageWindow{Label: "weekly", UsedPercent: payload.Usage.UsedPercent})
	}
	return info, nil
}

// MinimaxProbe reads the coding-plan remains endpoint:
// model_remains[].general carries the 5h and weekly remaining
// percentages (used = 100 - remain).
type MinimaxProbe struct{}

func (MinimaxProbe) Vendor() string { return "minimax" }

func (MinimaxProbe) MatchesURL(host string) bool {
	return hostMatch(host, "api.minimax.io", "api.minimaxi.com")
}

func (MinimaxProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	var payload struct {
		ModelRemains []struct {
			General struct {
				FiveHourRemainPercent float64 `json:"five_hour_remain_percent"`
				WeeklyRemainPercent   float64 `json:"weekly_remain_percent"`
			} `json:"general"`
		} `json:"model_remains"`
	}
	if err := getJSON(ctx, base+"/v1/api/openplatform/coding_plan/remains", apiKey, &payload); err != nil {
		return nil, err
	}
	info := &UsageInfo{Vendor: "minimax", Source: "coding_plan/remains"}
	if len(payload.ModelRemains) > 0 {
		g := payload.ModelRemains[0].General
		if g.FiveHourRemainPercent > 0 {
			info.Windows = append(info.Windows, UsageWindow{Label: "5h", UsedPercent: clampPercent(100 - g.FiveHourRemainPercent)})
		}
		if g.WeeklyRemainPercent > 0 {
			info.Windows = append(info.Windows, UsageWindow{Label: "weekly", UsedPercent: clampPercent(100 - g.WeeklyRemainPercent)})
		}
	}
	return info, nil
}

// AnthropicOAuthProbe reads the OAuth usage beta endpoint; the session
// token rides the same Authorization header, plus the beta header.
type AnthropicOAuthProbe struct{}

func (AnthropicOAuthProbe) Vendor() string { return "anthropic-oauth" }

func (AnthropicOAuthProbe) MatchesURL(host string) bool { return hostMatch(host, "api.anthropic.com") }

func (AnthropicOAuthProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/oauth/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		// #2366-2: this probe bypasses getJSON (beta header), so the only
		// RateLimitedError producer never ran for anthropic-oauth - the
		// Retry-After-sized negative cache (#2360) degraded to plain 1min
		// negativeTTL and every-minute re-probing survived for this vendor.
		return nil, &RateLimitedError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage probe %s: status %d", base+"/api/oauth/usage", resp.StatusCode)
	}
	var payload struct {
		FiveHour struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
	}
	if err := decodeJSON(resp, &payload); err != nil {
		return nil, err
	}
	info := &UsageInfo{Vendor: "anthropic-oauth", Source: "oauth/usage"}
	if payload.FiveHour.Utilization > 0 {
		w := UsageWindow{Label: "5h", UsedPercent: clampPercent(payload.FiveHour.Utilization * 100)}
		w.ResetsAt = parseResetTime(payload.FiveHour.ResetsAt)
		info.Windows = append(info.Windows, w)
	}
	if payload.SevenDay.Utilization > 0 {
		w := UsageWindow{Label: "7d", UsedPercent: clampPercent(payload.SevenDay.Utilization * 100)}
		w.ResetsAt = parseResetTime(payload.SevenDay.ResetsAt)
		info.Windows = append(info.Windows, w)
	}
	return info, nil
}

// OpenrouterProbe reads /api/v1/credits; when the account key lacks
// permission for account-wide credits it degrades to the per-key limit
// endpoint (openrouter-credits experience).
type OpenrouterProbe struct{}

func (OpenrouterProbe) Vendor() string { return "openrouter" }

func (OpenrouterProbe) MatchesURL(host string) bool { return hostMatch(host, "openrouter.ai") }

func (OpenrouterProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	if !strings.Contains(base, "openrouter.ai") && !strings.HasPrefix(base, "http") {
		base = "https://openrouter.ai"
	}
	var payload struct {
		Data struct {
			TotalCredits float64 `json:"total_credits"`
			TotalUsage   float64 `json:"total_usage"`
		} `json:"data"`
	}
	creditsErr := getJSON(ctx, base+"/api/v1/credits", apiKey, &payload)
	var rl *RateLimitedError
	if creditsErr == nil {
		b := payload.Data.TotalCredits - payload.Data.TotalUsage
		return &UsageInfo{Vendor: "openrouter", Balance: f64(b), Source: "credits"}, nil
	}
	if errors.As(creditsErr, &rl) {
		// #2366 part 3: a rate-limited credits endpoint must not fall through
		// to the limit endpoint (that would double-request a throttled host),
		// and must surface Retry-After so the Service sizes the negative cache.
		return nil, creditsErr
	}
	// Degrade: per-key usage limit.
	var keyPayload struct {
		Data struct {
			UsageLimit float64 `json:"usage_limit"`
			Usage      float64 `json:"usage"`
		} `json:"data"`
	}
	if err := getJSON(ctx, base+"/api/v1/limit", apiKey, &keyPayload); err != nil {
		return nil, err
	}
	info := &UsageInfo{Vendor: "openrouter", Source: "key-limit"}
	if keyPayload.Data.UsageLimit > 0 {
		info.Windows = append(info.Windows, UsageWindow{
			Label:       "key",
			UsedPercent: clampPercent(100 * keyPayload.Data.Usage / keyPayload.Data.UsageLimit),
		})
	}
	return info, nil
}

// SiliconflowProbe reads /v1/user/info: data.balance.
type SiliconflowProbe struct{}

func (SiliconflowProbe) Vendor() string { return "siliconflow" }

func (SiliconflowProbe) MatchesURL(host string) bool {
	return hostMatch(host, "api.siliconflow.cn", "api.siliconflow.com")
}

func (SiliconflowProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	var payload struct {
		Data struct {
			Balance float64 `json:"balance"`
		} `json:"data"`
	}
	if err := getJSON(ctx, base+"/v1/user/info", apiKey, &payload); err != nil {
		return nil, err
	}
	return &UsageInfo{Vendor: "siliconflow", Balance: f64(payload.Data.Balance), Source: "user/info"}, nil
}
