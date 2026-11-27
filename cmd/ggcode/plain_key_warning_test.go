package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// The plain (no-TUI) frontend used to start silently with no credential: the
// user only found out when the first message returned 401.
func TestWarnPlainFrontendMissingKey_WarnsWhenUnusable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "")

	cfg := &config.Config{
		Vendor:   "deepseek",
		Endpoint: "api",
		Model:    "deepseek-chat",
		Vendors: map[string]config.VendorConfig{
			"deepseek": {
				DisplayName: "DeepSeek",
				APIKey:      "${DEEPSEEK_API_KEY}",
				Endpoints: map[string]config.EndpointConfig{
					"api": {DisplayName: "DeepSeek API", Protocol: "openai", BaseURL: "https://api.deepseek.com/v1", DefaultModel: "deepseek-chat", Models: []string{"deepseek-chat"}},
				},
			},
		},
	}

	var buf bytes.Buffer
	if !warnPlainFrontendMissingKey(&buf, cfg) {
		t.Fatal("want a warning when the endpoint has no usable credential")
	}
	out := buf.String()
	for _, want := range []string{
		"no usable API key",
		"deepseek/api",
		"ggcode onboard",
		"DEEPSEEK_API_KEY",
		filepath.Join(home, ".ggcode", "keys.env"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("warning must mention %q, got:\n%s", want, out)
		}
	}
}

func TestWarnPlainFrontendMissingKey_SilentWhenUsable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cases := map[string]*config.Config{
		"plaintext key": {
			Vendor: "deepseek", Endpoint: "api", Model: "deepseek-chat",
			Vendors: map[string]config.VendorConfig{"deepseek": {
				APIKey: "sk-present",
				Endpoints: map[string]config.EndpointConfig{
					"api": {Protocol: "openai", BaseURL: "https://api.deepseek.com/v1", DefaultModel: "deepseek-chat", Models: []string{"deepseek-chat"}},
				},
			}},
		},
		"loopback endpoint": {
			Vendor: "local", Endpoint: "api", Model: "local-model",
			Vendors: map[string]config.VendorConfig{"local": {
				Endpoints: map[string]config.EndpointConfig{
					"api": {Protocol: "openai", BaseURL: "http://127.0.0.1:11434/v1", DefaultModel: "local-model", Models: []string{"local-model"}},
				},
			}},
		},
		"oauth endpoint": {
			Vendor: "v", Endpoint: "api", Model: "m",
			Vendors: map[string]config.VendorConfig{"v": {
				Endpoints: map[string]config.EndpointConfig{
					"api": {Protocol: "openai", BaseURL: "https://api.example.com/v1", AuthType: "oauth", DefaultModel: "m", Models: []string{"m"}},
				},
			}},
		},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if warnPlainFrontendMissingKey(&buf, cfg) {
				t.Errorf("no warning expected, got:\n%s", buf.String())
			}
			if buf.Len() != 0 {
				t.Errorf("silent case wrote output: %q", buf.String())
			}
		})
	}
	if warnPlainFrontendMissingKey(&bytes.Buffer{}, nil) {
		t.Error("nil config must not warn")
	}
}
