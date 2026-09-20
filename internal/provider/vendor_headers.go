package provider

import (
	"net/http"
	"net/url"
	"strings"
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
	return headers
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
