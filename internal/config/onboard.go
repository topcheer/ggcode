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
//
// OWNER INVARIANT (v1.3.234, reported by the repo owner): whether onboarding
// runs is decided SOLELY by the presence of the ggcode.yaml config file. If
// the file exists, onboarding must never trigger again - no vendor/endpoint/
// API-key inference may override that. The inference below only runs for
// configs with no file on disk (fresh installs, in-memory test configs).
// History: key persistence moved to keys.env (#1517) left ${VAR} references
// in yaml; if an entrypoint skipped loadRuntimeEnv, LookupEnv failed and
// every existing user was thrown back into onboarding after upgrading.
// File presence is the single source of truth; do not reintroduce inference
// on top of an existing file.
func (c *Config) NeedsOnboard() bool {
	if c == nil {
		return true
	}

	if c.FilePath != "" {
		if _, err := os.Stat(c.FilePath); err == nil {
			return false
		}
	}

	if strings.TrimSpace(c.Vendor) == "" {
		return true
	}

	resolved, err := c.ResolveActiveEndpoint()
	if err != nil {
		return true
	}

	return !hasUsableAPIKey(resolved.APIKey)
}

// hasUsableAPIKey returns true if the key is a real value (not empty and not
// an unresolvable ${VAR} reference).
func hasUsableAPIKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}

	if envRefPattern.MatchString(key) {
		varName := key[2 : len(key)-1]
		_, ok := os.LookupEnv(varName)
		return ok
	}
	return true
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

		epIDs := make([]string, 0, len(vc.Endpoints))
		for epID := range vc.Endpoints {
			epIDs = append(epIDs, epID)
		}
		sort.Strings(epIDs)

		defaultEp := ""
		for _, epID := range epIDs {
			ep := vc.Endpoints[epID]
			if len(ep.Tags) == 0 || ep.Tags[0] != "official" {
				continue
			}
			if hasEndpointTag(ep, "enterprise") {
				continue
			}
			defaultEp = epID
			break
		}
		if defaultEp == "" {
			defaultEp = epIDs[0]
		}
		for _, epID := range epIDs {
			ep := vc.Endpoints[epID]
			if vp.DefaultEndpoint == "" {
				vp.DefaultEndpoint = defaultEp
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

		if len(vp.Endpoints) == 0 {
			continue
		}
		out = append(out, vp)
	}

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

// hasEndpointTag reports whether ep carries the given tag (#1712).
func hasEndpointTag(ep EndpointConfig, tag string) bool {
	for _, t := range ep.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
