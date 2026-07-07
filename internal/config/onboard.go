package config

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

// envRefPattern matches ${VAR} style environment variable references.
var envRefPattern = regexp.MustCompile(`^\$\{[^}]+\}$`)

// VendorPreset describes a built-in vendor template for the onboard wizard.
type VendorPreset struct {
	ID              string // vendor key, e.g. "anthropic"
	DisplayName     string // human-readable, e.g. "Anthropic"
	APIKeyEnvHint   string // env var hint, e.g. "ANTHROPIC_API_KEY"
	DefaultEndpoint string // first endpoint key
	NeedsAPIKey     bool   // false for oauth-only vendors like github-copilot
	Endpoints       []EndpointPreset
}

// EndpointPreset describes a built-in endpoint within a vendor.
type EndpointPreset struct {
	ID           string
	DisplayName  string
	Protocol     string
	BaseURL      string
	DefaultModel string
	Models       []string
}

// NeedsOnboard returns true when the current config does not have a usable
// LLM provider and the user should be guided through first-time setup.
func (c *Config) NeedsOnboard() bool {
	if c == nil {
		return true
	}
	// No vendor selected at all.
	if strings.TrimSpace(c.Vendor) == "" {
		return true
	}
	// Try to resolve; any error means incomplete config.
	resolved, err := c.ResolveActiveEndpoint()
	if err != nil {
		return true
	}
	// Resolved but no usable API key.
	return !hasUsableAPIKey(resolved.APIKey)
}

// hasUsableAPIKey returns true if the key is a real value (not empty and not
// an unresolvable ${VAR} reference).
func hasUsableAPIKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	// If it looks like a ${VAR} reference, check whether the env var exists.
	if envRefPattern.MatchString(key) {
		varName := key[2 : len(key)-1]
		_, ok := os.LookupEnv(varName)
		return ok
	}
	return true // plain key, usable
}

// VendorPresets returns all built-in vendor templates from DefaultConfig,
// suitable for displaying in the onboard wizard.
func VendorPresets() []VendorPreset {
	dc := DefaultConfig()
	if dc == nil {
		return nil
	}
	out := make([]VendorPreset, 0, len(dc.Vendors))
	for id, vc := range dc.Vendors {
		vp := VendorPreset{
			ID:            id,
			DisplayName:   vc.DisplayName,
			APIKeyEnvHint: extractEnvVarName(vc.APIKey),
			NeedsAPIKey:   vc.APIKey != "",
		}
		for epID, ep := range vc.Endpoints {
			if vp.DefaultEndpoint == "" {
				vp.DefaultEndpoint = epID
			}
			vp.Endpoints = append(vp.Endpoints, EndpointPreset{
				ID:           epID,
				DisplayName:  ep.DisplayName,
				Protocol:     ep.Protocol,
				BaseURL:      ep.BaseURL,
				DefaultModel: ep.DefaultModel,
				Models:       ep.Models,
			})
		}
		// Skip vendors with no endpoints — they crash the onboard UI.
		if len(vp.Endpoints) == 0 {
			continue
		}
		out = append(out, vp)
	}
	// Sort by display name for consistent ordering.
	sortVendors(out)
	return out
}

// extractEnvVarName returns the env var name from a "${VAR}" style reference,
// or the raw string if it doesn't look like a var ref.
func extractEnvVarName(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		return s[2 : len(s)-1]
	}
	return s
}

func sortVendors(vs []VendorPreset) {
	sort.Slice(vs, func(i, j int) bool {
		return vs[i].DisplayName < vs[j].DisplayName
	})
}
