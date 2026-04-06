package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const defaultBootstrapAnthropicModel = "opus-4.6"

var bootstrapVendorIDSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

type claudeSettingsFile struct {
	Env map[string]string `json:"env"`
}

func applyFirstLaunchAnthropicBootstrap(cfg *Config) bool {
	if cfg == nil {
		return false
	}

	baseURL := strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
	authVar, authValue := preferredAnthropicCredential()
	if baseURL == "" || authValue == "" {
		return false
	}

	claudeEnv := loadClaudeEnv()
	model := firstNonEmpty(
		strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")),
		strings.TrimSpace(claudeEnv["ANTHROPIC_MODEL"]),
		strings.TrimSpace(claudeEnv["ANTHROPIC_DEFAULT_OPUS_MODEL"]),
		defaultBootstrapAnthropicModel,
	)

	vendorID, endpointID := upsertAnthropicBootstrapVendor(cfg, baseURL, authValue, model, authVar)
	if vendorID == "" || endpointID == "" {
		return false
	}
	cfg.Vendor = vendorID
	cfg.Endpoint = endpointID
	cfg.Model = model
	return true
}

func preferredAnthropicCredential() (string, string) {
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")); value != "" {
		return "ANTHROPIC_AUTH_TOKEN", value
	}
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); value != "" {
		return "ANTHROPIC_API_KEY", value
	}
	return "", ""
}

func loadClaudeEnv() map[string]string {
	out := map[string]string{}
	for _, path := range knownClaudeSettingsPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}
		var parsed claudeSettingsFile
		if err := json.Unmarshal(data, &parsed); err != nil {
			continue
		}
		for key, value := range parsed.Env {
			if _, exists := out[key]; exists {
				continue
			}
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

func knownClaudeSettingsPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".claude.json"),
	}
}

func upsertAnthropicBootstrapVendor(cfg *Config, baseURL, apiKey, model, authVar string) (string, string) {
	normalizedBaseURL := normalizeBaseURL(baseURL)
	if normalizedBaseURL == "" {
		normalizedBaseURL = strings.TrimSpace(baseURL)
	}

	for vendorID, vendor := range cfg.Vendors {
		for endpointID, ep := range vendor.Endpoints {
			if strings.TrimSpace(ep.Protocol) != "anthropic" {
				continue
			}
			if normalizeBaseURL(ep.BaseURL) != normalizedBaseURL {
				continue
			}
			ep.APIKey = apiKey
			if ep.DefaultModel == "" {
				ep.DefaultModel = model
			}
			ep.SelectedModel = model
			if !slices.Contains(ep.Models, model) {
				ep.Models = append(ep.Models, model)
			}
			vendor.Endpoints[endpointID] = ep
			cfg.Vendors[vendorID] = vendor
			return vendorID, endpointID
		}
	}

	vendorID := uniqueBootstrapVendorID(cfg, inferBootstrapVendorID(baseURL))
	displayName := inferBootstrapVendorDisplayName(vendorID)
	endpointID := "env-anthropic"
	cfg.Vendors[vendorID] = VendorConfig{
		DisplayName: displayName,
		APIKey:      apiKey,
		Endpoints: map[string]EndpointConfig{
			endpointID: {
				DisplayName:   displayName + " (Anthropic)",
				Protocol:      "anthropic",
				BaseURL:       normalizedBaseURL,
				APIKey:        apiKey,
				DefaultModel:  model,
				SelectedModel: model,
				Models:        []string{model},
				ContextWindow: inferContextWindow(model, "anthropic"),
				MaxTokens:     inferMaxOutputTokens(model, "anthropic"),
				Tags:          []string{"bootstrapped", "anthropic", strings.ToLower(authVar)},
			},
		},
	}
	return vendorID, endpointID
}

func normalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func inferBootstrapVendorID(rawURL string) string {
	host := strings.TrimSpace(rawURL)
	if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return "anthropic-env"
	}
	parts := strings.Split(host, ".")
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) == 0 {
		return "anthropic-env"
	}
	if len(filtered) >= 3 && len(filtered[len(filtered)-1]) == 2 && len(filtered[len(filtered)-2]) <= 3 {
		filtered = filtered[:len(filtered)-1]
	}
	candidate := filtered[0]
	if len(filtered) >= 2 {
		candidate = filtered[len(filtered)-2]
	}
	if len(filtered) >= 3 && isCommonHostPrefix(candidate) {
		candidate = filtered[len(filtered)-3]
	}
	candidate = sanitizeBootstrapVendorID(candidate)
	if candidate == "" {
		return "anthropic-env"
	}
	return candidate
}

func inferBootstrapVendorDisplayName(vendorID string) string {
	if vendorID == "" {
		return "Anthropic Env"
	}
	return strings.ToUpper(vendorID[:1]) + vendorID[1:]
}

func uniqueBootstrapVendorID(cfg *Config, base string) string {
	if cfg == nil {
		return base
	}
	id := sanitizeBootstrapVendorID(base)
	if id == "" {
		id = "anthropic-env"
	}
	if _, exists := cfg.Vendors[id]; !exists {
		return id
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", id, i)
		if _, exists := cfg.Vendors[candidate]; !exists {
			return candidate
		}
	}
}

func sanitizeBootstrapVendorID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	value = bootstrapVendorIDSanitizer.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	return value
}

func isCommonHostPrefix(part string) bool {
	switch part {
	case "api", "open", "router", "gateway", "www":
		return true
	default:
		return false
	}
}
