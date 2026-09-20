package provider

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/topcheer/ggcode/internal/auth"
)

func vendorSpecificAuthHeaders(baseURL, apiKey string) http.Header {
	headers := make(http.Header)
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return headers
	}
	if isXiaomiMiMoBaseURL(baseURL) {
		headers.Set("api-key", apiKey)
	}
	// OpenCode inference gateway: OAuth console tokens require the org ID
	// header (verified: same token 401s without it, authenticates with it).
	// Only attached when the key IS the stored OAuth token - console-issued
	// API keys on the legacy zen gateway keep their old behavior.
	if isOpenCodeBaseURL(baseURL) {
		if info, err := auth.DefaultStore().Load(auth.ProviderOpenCode); err == nil && info != nil {
			if apiKey == strings.TrimSpace(info.AccessToken) && strings.TrimSpace(info.OrgID) != "" {
				headers.Set("x-opencode-org-id", strings.TrimSpace(info.OrgID))
			}
		}
	}
	return headers
}

func isOpenCodeBaseURL(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	return host == "opencode.ai" || strings.HasSuffix(host, ".opencode.ai")
}

func isXiaomiMiMoBaseURL(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	return host == "xiaomimimo.com" || strings.HasSuffix(host, ".xiaomimimo.com")
}

func isOpenRouterEndpoint(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}
