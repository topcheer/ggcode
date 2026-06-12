package provider

import "testing"

func TestVendorSpecificAuthHeaders_XiaomiMiMoAddsAPIKey(t *testing.T) {
	headers := vendorSpecificAuthHeaders("https://token-plan-cn.xiaomimimo.com/anthropic", "tp-test")
	if got := headers.Get("api-key"); got != "tp-test" {
		t.Fatalf("expected api-key header for xiaomimimo host, got %q", got)
	}
}

func TestVendorSpecificAuthHeaders_OtherHostsStayEmpty(t *testing.T) {
	headers := vendorSpecificAuthHeaders("https://api.openai.com/v1", "sk-test")
	if got := headers.Get("api-key"); got != "" {
		t.Fatalf("expected no api-key header for non-xiaomimimo host, got %q", got)
	}
}

func TestIsXiaomiMiMoBaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    bool
	}{
		{baseURL: "https://token-plan-cn.xiaomimimo.com/v1", want: true},
		{baseURL: "https://platform.xiaomimimo.com/docs", want: true},
		{baseURL: "https://api.openai.com/v1", want: false},
		{baseURL: "not a url", want: false},
	}
	for _, tc := range tests {
		if got := isXiaomiMiMoBaseURL(tc.baseURL); got != tc.want {
			t.Fatalf("isXiaomiMiMoBaseURL(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}
