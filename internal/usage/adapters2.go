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

type KimiProbe struct{}

func (KimiProbe) Vendor() string { return "kimi" }

func (KimiProbe) MatchesURL(baseURL string) bool {
	return hostMatch(urlHostOf(baseURL), "api.kimi.com", "kimi.com")
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
				UsedRatio float64 `json:"used_ratio"`
				ResetTime string  `json:"reset_time"`
			} `json:"limit_5h"`
			Limit7d struct {
				UsedRatio float64 `json:"used_ratio"`
				ResetTime string  `json:"reset_time"`
			} `json:"limit_7d"`
		} `json:"usages"`
	}

	for _, suffix := range []string{"/coding/v1", "/coding", "/v1"} {
		base = strings.TrimSuffix(base, suffix)
	}
	if err := getJSON(ctx, base+"/coding/v1/usages", apiKey, &payload); err != nil {
		return nil, err
	}

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

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

type MinimaxProbe struct{}

func (MinimaxProbe) Vendor() string { return "minimax" }

func (MinimaxProbe) MatchesURL(baseURL string) bool {
	return hostMatch(urlHostOf(baseURL), "api.minimax.io", "api.minimaxi.com")
}

func (MinimaxProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")

	for _, suffix := range []string{"/anthropic", "/v1"} {
		base = strings.TrimSuffix(base, suffix)
	}
	var payload struct {
		ModelRemains []struct {
			ModelName string `json:"model_name"`

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

		if mr.EndTimeMs > 0 || mr.CurrentIntervalRemainingPercent > 0 {
			w := UsageWindow{Label: "5h", UsedPercent: clampPercent(100 - mr.CurrentIntervalRemainingPercent)}
			if mr.EndTimeMs > 0 {
				w.ResetsAt = time.UnixMilli(mr.EndTimeMs)
			}
			info.Windows = append(info.Windows, w)
		}

		if mr.CurrentWeeklyStatus == 1 && (mr.WeeklyEndTimeMs > 0 || mr.CurrentWeeklyRemainingPercent > 0) {
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

type AnthropicOAuthProbe struct{}

func (AnthropicOAuthProbe) Vendor() string { return "anthropic-oauth" }

func (AnthropicOAuthProbe) MatchesURL(baseURL string) bool {
	return hostMatch(urlHostOf(baseURL), "api.anthropic.com")
}

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

type OpenrouterProbe struct{}

func (OpenrouterProbe) Vendor() string { return "openrouter" }

func (OpenrouterProbe) MatchesURL(baseURL string) bool {
	return hostMatch(urlHostOf(baseURL), "openrouter.ai")
}

func (OpenrouterProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")

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

		return nil, creditsErr
	}

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

type SiliconflowProbe struct{}

func (SiliconflowProbe) Vendor() string { return "siliconflow" }

func (SiliconflowProbe) MatchesURL(baseURL string) bool {
	return hostMatch(urlHostOf(baseURL), "api.siliconflow.cn", "api.siliconflow.com")
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
