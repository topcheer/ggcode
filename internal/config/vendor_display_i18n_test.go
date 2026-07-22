package config

import "testing"

func TestLocalizedVendorDisplay(t *testing.T) {
	tests := []struct {
		vendorID string
		english  string
		lang     string
		want     string
	}{
		{"zai", "Z.ai", "zh-CN", "智谱 Z.AI"},
		{"zai", "Z.ai", "en", "Z.ai"},
		{"aliyun", "Aliyun Bailian Coding Plan", "zh-CN", "阿里云百炼"},
		{"aliyun", "Aliyun Bailian Coding Plan", "zh-TW", "阿里云百炼"},
		{"aliyun", "Aliyun Bailian Coding Plan", "en", "Aliyun Bailian Coding Plan"},
		{"ark", "Volcengine Ark Coding Plan", "zh-CN", "火山引擎方舟"},
		{"openai", "OpenAI", "zh-CN", "OpenAI"}, // not in map, fallback to english
		{"custom-vendor", "My Custom Vendor", "zh-CN", "My Custom Vendor"},
	}
	for _, tt := range tests {
		got := localizedVendorDisplay(tt.vendorID, tt.english, tt.lang)
		if got != tt.want {
			t.Errorf("localizedVendorDisplay(%q, %q, %q) = %q, want %q",
				tt.vendorID, tt.english, tt.lang, got, tt.want)
		}
	}
}

func TestLocalizedEndpointDisplay(t *testing.T) {
	tests := []struct {
		vendorID   string
		endpointID string
		english    string
		lang       string
		want       string
	}{
		{"zai", "cn-coding-openai", "CN Coding Plan", "zh-CN", "国内编程套餐"},
		{"zai", "cn-coding-openai", "CN Coding Plan", "en", "CN Coding Plan"},
		{"kimi", "coding-openai", "Kimi Coding Plan", "zh-CN", "Kimi 编程套餐"},
		{"ark", "coding-anthropic", "Ark Coding Plan (Anthropic)", "zh-CN", "方舟编程套餐 (Anthropic)"},
		{"custom", "my-endpoint", "My Custom Endpoint", "zh-CN", "My Custom Endpoint"},
	}
	for _, tt := range tests {
		got := localizedEndpointDisplay(tt.vendorID, tt.endpointID, tt.english, tt.lang)
		if got != tt.want {
			t.Errorf("localizedEndpointDisplay(%q, %q, %q, %q) = %q, want %q",
				tt.vendorID, tt.endpointID, tt.english, tt.lang, got, tt.want)
		}
	}
}

func TestResolveDisplayName(t *testing.T) {
	cfg := DefaultConfig()

	// English
	cfg.Language = "en"
	v, e := cfg.ResolveDisplayName("zai", "cn-coding-openai")
	if v != "Z.ai" {
		t.Errorf("en vendor: got %q, want Z.ai", v)
	}
	if e != "CN Coding Plan" {
		t.Errorf("en endpoint: got %q, want CN Coding Plan", e)
	}

	// Chinese
	cfg.Language = "zh-CN"
	v, e = cfg.ResolveDisplayName("zai", "cn-coding-openai")
	if v != "智谱 Z.AI" {
		t.Errorf("zh vendor: got %q, want 智谱 Z.AI", v)
	}
	if e != "国内编程套餐" {
		t.Errorf("zh endpoint: got %q, want 国内编程套餐", e)
	}

	// Unknown vendor/endpoint should fall back to raw keys
	v, e = cfg.ResolveDisplayName("custom-vendor", "custom-endpoint")
	if v != "custom-vendor" {
		t.Errorf("unknown vendor: got %q, want custom-vendor", v)
	}
	if e != "custom-endpoint" {
		t.Errorf("unknown endpoint: got %q, want custom-endpoint", e)
	}
}

func TestResolveEndpointSelection_LocalizedDisplay(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "zai"
	cfg.Endpoint = "cn-coding-openai"
	cfg.Model = "glm-5-turbo"

	// English: display names unchanged
	cfg.Language = "en"
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if resolved.VendorName != "Z.ai" {
		t.Errorf("en VendorName: got %q, want Z.ai", resolved.VendorName)
	}
	if resolved.EndpointName != "CN Coding Plan" {
		t.Errorf("en EndpointName: got %q, want CN Coding Plan", resolved.EndpointName)
	}

	// Chinese: display names localized
	cfg.Language = "zh-CN"
	resolved, err = cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if resolved.VendorName != "智谱 Z.AI" {
		t.Errorf("zh VendorName: got %q, want 智谱 Z.AI", resolved.VendorName)
	}
	if resolved.EndpointName != "国内编程套餐" {
		t.Errorf("zh EndpointName: got %q, want 国内编程套餐", resolved.EndpointName)
	}
}
