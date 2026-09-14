package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("usage probe %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func f64(v float64) *float64 { return &v }

// ZaiProbe queries the bigmodel/z.ai quota endpoint (sub2api-verified
// shape): a limit/usage pair the sidebar renders as a window.
type ZaiProbe struct{}

func (ZaiProbe) Vendor() string { return "zai" }

func (ZaiProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
	// The chat base is .../api/paas/v4; the monitor endpoint sits at the
	// site root. Normalize both known hosts.
	for _, suffix := range []string{"/api/paas/v4", "/v4"} {
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
		}
	}
	var payload struct {
		LimitUsed  float64 `json:"limit_used"`
		LimitTotal float64 `json:"limit_total"`
	}
	if err := getJSON(ctx, base+"/api/monitor/usage/quota/limit", apiKey, &payload); err != nil {
		return nil, err
	}
	info := &UsageInfo{Vendor: "zai", Source: "quota/limit"}
	if payload.LimitTotal > 0 {
		info.Windows = append(info.Windows, UsageWindow{
			Label:       "quota",
			UsedPercent: 100 * payload.LimitUsed / payload.LimitTotal,
		})
	}
	return info, nil
}

// DeepSeekProbe reads the multi-currency balance table; USD first, else
// the first entry.
type DeepSeekProbe struct{}

func (DeepSeekProbe) Vendor() string { return "deepseek" }

func (DeepSeekProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
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

func (MoonshotProbe) Fetch(ctx context.Context, baseURL, apiKey string) (*UsageInfo, error) {
	base := strings.TrimRight(baseURL, "/")
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
