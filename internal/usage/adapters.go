package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// getJSON is the shared probe transport: Bearer auth, JSON body, bounded
// by the service timeout via ctx.
func getJSON(ctx context.Context, url, apiKey string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		// #2150 batch 2b: surface the server's Retry-After so the Service
		// can size its negative cache to the rate-limit window instead of
		// hammering the endpoint every negativeTTL tick.
		return &RateLimitedError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("usage probe %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// RateLimitedError marks a 429 response. The Service turns RetryAfter into
// the negative-cache lifetime (clamped), so a rate-limited vendor is probed
// again only when the server says it is allowed.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited (retry after %s)", e.RetryAfter)
	}
	return "rate limited"
}

// parseRetryAfter accepts both RFC 7231 forms: delta-seconds and
// HTTP-date. Returns 0 when absent/unparseable (caller then uses the
// default negative TTL).
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func f64(v float64) *float64 { return &v }

// ZaiProbe queries the bigmodel/z.ai quota endpoint (sub2api-verified
// shape): a limit/usage pair the sidebar renders as a window.
type ZaiProbe struct{}

func (ZaiProbe) Vendor() string { return "zai" }

// MatchesURL: zai accounts span two hosts - open.bigmodel.cn (mainland)
// and api.z.ai (international). Both share the same monitor endpoint.
func (ZaiProbe) MatchesURL(host string) bool {
	return hostMatch(host, "open.bigmodel.cn", "api.z.ai", "z.ai")
}

func (ZaiProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	// Chat bases come in four shapes (see ggcode.example.yaml):
	//   https://open.bigmodel.cn/api/paas/v4          (standard, mainland)
	//   https://open.bigmodel.cn/api/coding/paas/v4   (coding plan)
	//   https://api.z.ai/api/paas/v4                  (intl standard)
	//   https://api.z.ai/api/coding/paas/v4           (intl coding plan)
	// plus Anthropic-compatible entries (.../api/anthropic). The monitor
	// endpoint sits at the SITE ROOT for all of them - normalize every
	// known suffix away or the request 404s (coding-plan users saw a
	// misleading "unsupported" panel before the coding suffix was added).
	for _, suffix := range []string{"/api/coding/paas/v4", "/api/paas/v4", "/api/anthropic", "/v4"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
		}
	}
	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Limits []struct {
				Type         string `json:"type"` // TIME_LIMIT (rolling) | TOKENS_LIMIT (weekly)
				Unit         int    `json:"unit"`
				Number       int    `json:"number"`
				Percentage   int    `json:"percentage"` // 0-100 used
				NextResetMs  int64  `json:"nextResetTime"`
				CurrentValue int64  `json:"currentValue"`
				Remaining    int64  `json:"remaining"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := getJSON(ctx, base+"/api/monitor/usage/quota/limit", apiKey, &payload); err != nil {
		return nil, err
	}
	// Ground-truth shape (live capture 2026-09-18, user key): the earlier
	// limit_used/limit_total struct came from sub2api and matched NOTHING
	// in the real payload - the probe silently returned zero windows and
	// the panel rendered an empty (wrong) entry.
	info := &UsageInfo{Vendor: "zai", Source: "quota/limit"}
	for _, l := range payload.Data.Limits {
		w := UsageWindow{UsedPercent: clampPercent(float64(l.Percentage))}
		switch l.Type {
		case "TIME_LIMIT":
			w.Label = fmt.Sprintf("%d%sw", l.Number, unitShort(l.Unit))
			if l.Unit == 5 && l.Number == 1 {
				w.Label = "5h" // the common rolling window
			}
		case "TOKENS_LIMIT":
			w.Label = "weekly"
		default:
			w.Label = strings.ToLower(strings.TrimSuffix(l.Type, "_LIMIT"))
		}
		if l.NextResetMs > 0 {
			w.ResetsAt = time.UnixMilli(l.NextResetMs)
		}
		info.Windows = append(info.Windows, w)
	}
	return info, nil
}

// unitShort maps zai's opaque unit codes onto display letters. Observed
// in the wild: unit=5 (hours). Unknown codes degrade to the raw number.
func unitShort(u int) string {
	if u == 5 {
		return "h"
	}
	return ""
}

// DeepSeekProbe reads the multi-currency balance table; USD first, else
// the first entry.
type DeepSeekProbe struct{}

func (DeepSeekProbe) Vendor() string { return "deepseek" }

func (DeepSeekProbe) MatchesURL(host string) bool { return hostMatch(host, "api.deepseek.com") }

func (DeepSeekProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	// Chat bases carry /v1 (https://api.deepseek.com/v1); the balance
	// endpoint lives at the SITE ROOT (/user/balance, no v1). Strip it or
	// the request becomes /v1/user/balance (404) -> "unsupported".
	base = strings.TrimSuffix(base, "/v1")
	var payload struct {
		BalanceInfos []struct {
			Currency     string  `json:"currency"`
			TotalBalance float64 `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := getJSON(ctx, base+"/user/balance", apiKey, &payload); err != nil {
		return nil, err
	}
	for _, b := range payload.BalanceInfos {
		if b.Currency == "USD" || len(payload.BalanceInfos) == 1 {
			return &UsageInfo{Vendor: "deepseek", Balance: f64(b.TotalBalance), Source: "user/balance"}, nil
		}
	}
	return &UsageInfo{Vendor: "deepseek", Source: "user/balance"}, nil
}

// MoonshotProbe reads the available_balance field.
type MoonshotProbe struct{}

func (MoonshotProbe) Vendor() string { return "moonshot" }

func (MoonshotProbe) MatchesURL(host string) bool {
	return hostMatch(host, "api.moonshot.cn", "api.moonshot.ai")
}

func (MoonshotProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	// Chat bases carry /v1 (https://api.moonshot.cn/v1); the balance
	// endpoint is /v1/users/me/balance - appended to the ROOT, not to the
	// /v1 base (that would double it: /v1/v1/users/... 404).
	base = strings.TrimSuffix(base, "/v1")
	var payload struct {
		Data struct {
			AvailableBalance float64 `json:"available_balance"`
		} `json:"data"`
	}
	if err := getJSON(ctx, base+"/v1/users/me/balance", apiKey, &payload); err != nil {
		return nil, err
	}
	return &UsageInfo{Vendor: "moonshot", Balance: f64(payload.Data.AvailableBalance), Source: "users/me/balance"}, nil
}

// DefaultService returns a Service with every P1 probe registered.
func DefaultService() *Service {
	s := NewService()
	s.Register(ZaiProbe{})
	s.Register(DeepSeekProbe{})
	s.Register(MoonshotProbe{})
	s.Register(KimiProbe{})
	s.Register(MinimaxProbe{})
	s.Register(AnthropicOAuthProbe{})
	s.Register(OpenrouterProbe{})
	s.Register(SiliconflowProbe{})
	return s
}

func hostMatch(host string, known ...string) bool {
	for _, k := range known {
		if host == k || strings.HasSuffix(host, "."+k) {
			return true
		}
	}
	return false
}
