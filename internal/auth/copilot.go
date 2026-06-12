package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultCopilotClientID          = "Ov23li61W929PYwUl7RD"
	oauthPollingSafetyMargin        = 3 * time.Second
	defaultCopilotDevicePollTimeout = 5 * time.Minute
)

type CopilotDeviceFlow struct {
	Domain          string
	EnterpriseURL   string
	VerificationURI string
	UserCode        string
	DeviceCode      string
	Interval        time.Duration
}

type copilotDeviceCodeResponse struct {
	VerificationURI string `json:"verification_uri"`
	UserCode        string `json:"user_code"`
	DeviceCode      string `json:"device_code"`
	Interval        int    `json:"interval"`
}

type copilotTokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
	Interval    int    `json:"interval"`
}

func CopilotAPIBaseURL(enterpriseURL string) string {
	enterpriseURL = strings.TrimSpace(enterpriseURL)
	if enterpriseURL == "" {
		return "https://api.githubcopilot.com"
	}
	return "https://copilot-api." + normalizeDomain(enterpriseURL)
}

func NormalizeEnterpriseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("enterprise url is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid enterprise url: %w", err)
	}
	if strings.TrimSpace(parsed.Hostname()) == "" {
		return "", fmt.Errorf("invalid enterprise url")
	}
	return parsed.Host, nil
}

func StartCopilotDeviceFlow(ctx context.Context, enterpriseURL string) (*CopilotDeviceFlow, error) {
	return startCopilotDeviceFlow(ctx, http.DefaultClient, enterpriseURL)
}

func PollCopilotDeviceFlow(ctx context.Context, flow *CopilotDeviceFlow) (*Info, error) {
	return pollCopilotDeviceFlow(ctx, http.DefaultClient, flow)
}

func startCopilotDeviceFlow(ctx context.Context, client *http.Client, enterpriseURL string) (*CopilotDeviceFlow, error) {
	domain := "github.com"
	normalizedEnterprise := ""
	if strings.TrimSpace(enterpriseURL) != "" {
		var err error
		normalizedEnterprise, err = NormalizeEnterpriseURL(enterpriseURL)
		if err != nil {
			return nil, err
		}
		domain = normalizedEnterprise
	}

	endpoint := fmt.Sprintf("https://%s/login/device/code", domain)
	payload := map[string]string{
		"client_id": copilotClientID(),
		"scope":     "read:user",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ggcode")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("starting copilot device flow: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("starting copilot device flow: status %d", resp.StatusCode)
	}
	var data copilotDeviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decoding copilot device flow: %w", err)
	}
	interval := time.Duration(data.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &CopilotDeviceFlow{
		Domain:          domain,
		EnterpriseURL:   normalizedEnterprise,
		VerificationURI: data.VerificationURI,
		UserCode:        data.UserCode,
		DeviceCode:      data.DeviceCode,
		Interval:        interval,
	}, nil
}

func pollCopilotDeviceFlow(ctx context.Context, client *http.Client, flow *CopilotDeviceFlow) (*Info, error) {
	if flow == nil {
		return nil, fmt.Errorf("device flow is nil")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCopilotDevicePollTimeout)
		defer cancel()
	}
	tokenURL := fmt.Sprintf("https://%s/login/oauth/access_token", flow.Domain)
	interval := flow.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		payload := map[string]string{
			"client_id":   copilotClientID(),
			"device_code": flow.DeviceCode,
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "ggcode")

		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("polling copilot access token: %w", err)
		}
		var data copilotTokenResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&data)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("polling copilot access token: status %d", resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decoding copilot access token: %w", decodeErr)
		}
		if strings.TrimSpace(data.AccessToken) != "" {
			return &Info{
				ProviderID:    ProviderGitHubCopilot,
				Type:          "oauth",
				AccessToken:   data.AccessToken,
				RefreshToken:  data.AccessToken,
				EnterpriseURL: strings.TrimSpace(flow.EnterpriseURL),
			}, nil
		}
		switch strings.TrimSpace(data.Error) {
		case "authorization_pending", "":
			sleepWithMargin(ctx, interval)
			continue
		case "slow_down":
			if data.Interval > 0 {
				interval = time.Duration(data.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
			sleepWithMargin(ctx, interval)
			continue
		default:
			return nil, fmt.Errorf("copilot login failed: %s", data.Error)
		}
	}
}

func copilotClientID() string {
	if value := strings.TrimSpace(os.Getenv("GGCODE_GITHUB_COPILOT_CLIENT_ID")); value != "" {
		return value
	}
	return defaultCopilotClientID
}

func sleepWithMargin(ctx context.Context, interval time.Duration) {
	wait := interval + oauthPollingSafetyMargin
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func normalizeDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	return strings.TrimSuffix(raw, "/")
}
