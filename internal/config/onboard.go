package config

import (
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
)

var envRefPattern = regexp.MustCompile(`^\$\{[^}]+\}$`)

type VendorPreset struct {
	ID              string
	DisplayName     string
	APIKeyEnvHint   string
	DefaultEndpoint string
	NeedsAPIKey     bool
	Endpoints       []EndpointPreset
}

type EndpointPreset struct {
	ID           string
	DisplayName  string
	Protocol     string
	BaseURL      string
	DefaultModel string
	Models       []string
}

// NeedsOnboard reports whether the interactive start must run the onboarding
// flow.
//
// Owner invariant #1865: once a config file exists on disk, the flow does NOT
// run again automatically - a user who configured ggcode by hand (or who runs
// a keyless local endpoint) must never be nagged into onboarding.
//
// The one exception is the marker an onboarding run itself leaves behind when
// it could not persist a usable API key (cmd/ggcode/onboard.go): that run did
// not configure anything, and without the marker the user was stuck - the file
// exists, so onboarding never reappeared, and every request failed 401
// "API 密钥无效或未配置". The marker is honoured only while the active endpoint
// is still unusable, and is cleared as soon as a key shows up (env var, keys.env
// edit, `ggcode onboard`).
func (c *Config) NeedsOnboard() bool {
	if c == nil {
		return true
	}

	if strings.TrimSpace(c.Vendor) == "" {
		return true
	}

	// GGCODE_SKIP_AUTOCONFIG is the documented "I manage configuration
	// myself" escape (headless units, local servers, CI).
	if skipAutoConfig() {
		return false
	}

	if c.OnboardIncomplete {
		if c.ActiveEndpointUsable() {
			c.OnboardIncomplete = false
			return false
		}
		return true
	}

	if c.FilePath != "" {
		if _, err := os.Stat(c.FilePath); err == nil {
			return false
		}
	}

	resolved, err := c.ResolveActiveEndpoint()
	if err != nil {
		return true
	}

	return !endpointUsable(resolved)
}

// ActiveEndpointUsable reports whether the selected endpoint can be called with
// what the config resolves to right now: an OAuth token, a keyless or loopback
// endpoint, or a usable API key (plaintext or a resolvable ${ENV} reference).
// Writers use it to tell whether an onboarding run actually configured a
// provider; NeedsOnboard uses it for the same question behind invariant #1865.
func (c *Config) ActiveEndpointUsable() bool {
	resolved, err := c.ResolveActiveEndpoint()
	if err != nil {
		return false
	}
	return endpointUsable(resolved)
}

// endpointUsable reports whether the resolved endpoint has the credential it
// needs: OAuth tokens and keyless/local endpoints need no API key.
func endpointUsable(r *ResolvedEndpoint) bool {
	if r == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(r.AuthType), "oauth") {
		return true
	}
	if endpointNeedsNoAPIKey(r) {
		return true
	}
	return hasUsableAPIKey(r.APIKey)
}

// endpointNeedsNoAPIKey reports whether the endpoint can be talked to without
// any credential: an explicit keyless auth type, or a loopback base URL (a
// local llama.cpp / Ollama / vLLM server).
func endpointNeedsNoAPIKey(r *ResolvedEndpoint) bool {
	if r == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(r.AuthType)) {
	case "none", "no_auth", "keyless":
		return true
	}
	base := strings.TrimSpace(r.BaseURL)
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	return isLoopbackHost(u.Hostname())
}

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

func hasEndpointTag(ep EndpointConfig, tag string) bool {
	for _, t := range ep.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
