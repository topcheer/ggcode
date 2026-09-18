package usage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// KimiProbe reads /v1/usages: the limits array carries rolling windows
// (detail names them: "5h"), the usage object carries the weekly quota
// usage. sub2api's parseKimiUsageTiers shape.
type KimiProbe struct{}

func (KimiProbe) Vendor() string { return "kimi" }

// MatchesURL: Kimi Code platform (coding plan) lives on api.kimi.com -
// a DIFFERENT host from the open platform (api.moonshot.cn/.ai, owned by
// MoonshotProbe). Keys are not interchangeable between the two platforms
// (sk-kimi-* vs sk-*), so the coding-plan host must resolve HERE or its
// users get "unsupported" (user-reported 2026-09-18). Single-owner rule
// (#2394) holds: kimi.com vs moonshot.* are disjoint hosts.
func (KimiProbe) MatchesURL(host string) bool {
	return hostMatch(host, "api.kimi.com", "kimi.com")
}

func (KimiProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	var payload struct {
		Usage struct {
			Limit     string `json:"limit"`
			Used      string `json:"used"`
			Remaining string `json:"remaining"`
			ResetTime string `json:"resetTime"`
		} `json:"usage"`
		Limits []struct {
			Window struct {
				Duration int    `json:"duration"`
				TimeUnit string `json:"timeUnit"`
			} `json:"window"`
			Detail struct {
				Limit     string `json:"limit"`
				Used      string `json:"used"`
				Remaining string `json:"remaining"`
				ResetTime string `json:"resetTime"`
			} `json:"detail"`
		} `json:"limits"`
		Usages struct {
			Limit5h struct {
				UsedRatio float64 `json:"used_ratio"` // 0-1
				ResetTime string  `json:"reset_time"`
			} `json:"limit_5h"`
			Limit7d struct {
				UsedRatio float64 `json:"used_ratio"`
				ResetTime string  `json:"reset_time"`
			} `json:"limit_7d"`
		} `json:"usages"`
	}
	// The usage endpoint lives at the fixed path /coding/v1/usages on the
	// api.kimi.com root. Chat bases come in several shapes
	// (.../coding/v1, .../coding/, bare host) - normalize them to the
	// root and append the constant path. The old code appended
	// /v1/usages to the configured base, producing
	// .../coding/v1/v1/usages (404) -> "unsupported" for every
	// coding-plan user (user-reported 2026-09-18).
	for _, suffix := range []string{"/coding/v1", "/coding", "/v1"} {
		base = strings.TrimSuffix(base, suffix)
	}
	if err := getJSON(ctx, base+"/coding/v1/usages", apiKey, &payload); err != nil {
		return nil, err
	}
	// Ground-truth shape (live capture 2026-09-18, user key): limits[].
	// detail is a nested OBJECT (the old flat string field failed to
	// unmarshal -> error for every coding-plan user).
	//
	// Windows follow sub2api's parseKimiUsageTiers (cc-switch-aligned)
	// rather than the usages.*.used_ratio extras: utilization derives
	// from (limit-remaining)/limit*100 on the DECIMAL STRINGS -
	// limits[0].detail is the 5h window, the top-level usage object is
	// the weekly one.
	info := &UsageInfo{Vendor: "kimi", Source: "coding/v1/usages"}
	if len(payload.Limits) > 0 {
		d := payload.Limits[0].Detail
		if lim := parseFloat(d.Limit); lim > 0 {
			w := UsageWindow{Label: "5h", UsedPercent: clampPercent(100 * (lim - parseFloat(d.Remaining)) / lim)}
			w.ResetsAt = parseResetTime(d.ResetTime)
			info.Windows = append(info.Windows, w)
		}
	}
	if lim := parseFloat(payload.Usage.Limit); lim > 0 {
		w := UsageWindow{Label: "weekly", UsedPercent: clampPercent(100 * (lim - parseFloat(payload.Usage.Remaining)) / lim)}
		w.ResetsAt = parseResetTime(payload.Usage.ResetTime)
		info.Windows = append(info.Windows, w)
	}
	return info, nil
}

// parseFloat parses a decimal string field ("100", "7.5") tolerantly.
func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
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
	// Chat bases carry /v1 or /anthropic (ggcode.example.yaml); the remains
	// endpoint appends its own /v1/api/openplatform/... to the ROOT.
	// Appending to a /v1 base doubles it (/v1/v1/api/... 404).
	for _, suffix := range []string{"/anthropic", "/v1"} {
		base = strings.TrimSuffix(base, suffix)
	}
	var payload struct {
		ModelRemains []struct {
			ModelName string `json:"model_name"`
			// Fields per sub2api parseMiniMaxUsageTiers (cc-switch-aligned):
			// remaining percentages (used = 100 - remain), ms-epoch ends.
			// Only the "general" entry (coding plan) counts - video etc.
			// are different meters.
			CurrentIntervalRemainingPercent float64 `json:"current_interval_remaining_percent"`
			EndTimeMs                       int64   `json:"end_time"`
			CurrentWeeklyStatus             int     `json:"current_weekly_status"`
			CurrentWeeklyRemainingPercent   float64 `json:"current_weekly_remaining_percent"`
			WeeklyEndTimeMs                 int64   `json:"weekly_end_time"`
		} `json:"model_remains"`
	}
	if err := getJSON(ctx, base+"/v1/api/openplatform/coding_plan/remains", apiKey, &payload); err != nil {
		return nil, err
	}
	info := &UsageInfo{Vendor: "minimax", Source: "coding_plan/remains"}
	for _, mr := range payload.ModelRemains {
		if !strings.EqualFold(strings.TrimSpace(mr.ModelName), "general") {
			continue
		}
		if mr.CurrentIntervalRemainingPercent > 0 {
			w := UsageWindow{Label: "5h", UsedPercent: clampPercent(100 - mr.CurrentIntervalRemainingPercent)}
			if mr.EndTimeMs > 0 {
				w.ResetsAt = time.UnixMilli(mr.EndTimeMs)
			}
			info.Windows = append(info.Windows, w)
		}
		// Weekly only exists while the plan's weekly tier is active
		// (status==1); showing it otherwise displays a stale/zero bucket.
		if mr.CurrentWeeklyStatus == 1 && mr.CurrentWeeklyRemainingPercent > 0 {
			w := UsageWindow{Label: "weekly", UsedPercent: clampPercent(100 - mr.CurrentWeeklyRemainingPercent)}
			if mr.WeeklyEndTimeMs > 0 {
				w.ResetsAt = time.UnixMilli(mr.WeeklyEndTimeMs)
			}
			info.Windows = append(info.Windows, w)
		}
		break
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
	// Chat bases carry /api/v1 (https://openrouter.ai/api/v1); the credits
	// endpoint is /api/v1/credits on the ROOT - strip the suffix or it
	// doubles (/api/v1/api/v1/credits 404).
	base = strings.TrimSuffix(base, "/api/v1")
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
