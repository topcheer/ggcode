package config

import (
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
