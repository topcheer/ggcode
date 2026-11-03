package config

import (
	"fmt"
	"sync"

	"github.com/topcheer/ggcode/internal/util"

	"github.com/topcheer/ggcode/internal/debug"
	"os"
	"strings"
)

var vendorsWriteMu sync.Mutex

func (c *Config) SetEndpointAPIKey(vendor, endpoint, apiKey string, vendorScoped bool) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}

	vendorsWriteMu.Lock()
	defer vendorsWriteMu.Unlock()
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}

	apiKey = strings.TrimSpace(apiKey)

	if !vendorScoped {
		if _, ok := vc.Endpoints[endpoint]; !ok {
			return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
		}
	}

	if _, isRef := envReferenceVarName(apiKey); isRef || apiKey == "" {
		if vendorScoped {
			if apiKey == "" {
				clearAPIKeyRefEnv(vc.APIKey)
				syncVendorKeyEnv(vendor, "")
			}
			vc.APIKey = apiKey
			c.Vendors[vendor] = vc
		} else {
			ep, ok := vc.Endpoints[endpoint]
			if !ok {
				return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
			}
			if apiKey == "" {
				clearAPIKeyRefEnv(ep.APIKey)
			}
			ep.APIKey = apiKey
			vc.Endpoints[endpoint] = ep
			c.Vendors[vendor] = vc
		}
		return nil
	}

	var envVarName string
	if vendorScoped {
		envVarName = preferredVendorAPIKeyEnvVar(vendor)
	} else {
		envVarName = preferredEndpointAPIKeyEnvVar(vendor, endpoint)
	}

	util.RegisterSecretEnv(envVarName)
	os.Setenv(envVarName, apiKey)

	if err := writeKeysEnv(map[string]string{envVarName: apiKey}); err != nil {
		return fmt.Errorf("persisting API key for %s/%s: %w", vendor, endpoint, err)
	}
	if vendorScoped {

		syncVendorKeyEnv(vendor, apiKey)
	}

	ref := "${" + envVarName + "}"
	if vendorScoped {
		vc.APIKey = ref
		c.Vendors[vendor] = vc
	} else {
		ep, ok := vc.Endpoints[endpoint]
		if !ok {
			return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
		}
		ep.APIKey = ref
		vc.Endpoints[endpoint] = ep
		c.Vendors[vendor] = vc
	}
	return nil
}

func syncVendorKeyEnv(vendor, apiKey string) {
	primary := preferredVendorAPIKeyEnvVar(vendor)
	sibling := preferredEndpointAPIKeyEnvVar(vendor, "default")
	if apiKey == "" {
		if err := removeKeysEnv([]string{primary, sibling}); err != nil {
			debug.Log("config", "failed to remove vendor key env %v: %v", []string{primary, sibling}, err)
		}
		os.Unsetenv(primary)
		os.Unsetenv(sibling)
		return
	}
	util.RegisterSecretEnv(primary, sibling)
	os.Setenv(primary, apiKey)
	if sibling != primary {
		os.Setenv(sibling, apiKey)
	}
	entries := map[string]string{primary: apiKey}
	if sibling != primary {
		entries[sibling] = apiKey
	}
	if err := writeKeysEnv(entries); err != nil {
		debug.Log("config", "failed to sync vendor key env for %s: %v", vendor, err)
	}
}

func clearAPIKeyRefEnv(ref string) {
	if name, ok := envReferenceVarName(ref); ok {
		if err := removeKeysEnv([]string{name}); err != nil {
			debug.Log("config", "failed to remove %s from keys.env: %v", name, err)
		}
		os.Unsetenv(name)
	}
}

func (c *Config) SetVendorAPIKey(vendor, apiKey string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}

	vendorsWriteMu.Lock()
	defer vendorsWriteMu.Unlock()
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q not found", vendor)
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {

		clearAPIKeyRefEnv(vc.APIKey)
		syncVendorKeyEnv(vendor, "")
		vc.APIKey = ""
	} else if _, isRef := envReferenceVarName(apiKey); isRef {
		vc.APIKey = apiKey
	} else {
		envVarName := preferredEndpointAPIKeyEnvVar(vendor, "default")
		util.RegisterSecretEnv(envVarName)
		os.Setenv(envVarName, apiKey)

		if err := writeKeysEnv(map[string]string{envVarName: apiKey}); err != nil {
			return fmt.Errorf("persisting API key for vendor %s: %w", vendor, err)
		}
		syncVendorKeyEnv(vendor, apiKey)
		vc.APIKey = "${" + envVarName + "}"
	}
	c.Vendors[vendor] = vc
	return nil
}

func (c *Config) UpsertVendorEndpoint(vendor, endpoint string) (changed bool) {
	if c == nil || vendor == "" || endpoint == "" {
		return false
	}
	vendorsWriteMu.Lock()
	defer vendorsWriteMu.Unlock()
	if c.Vendors == nil {
		c.Vendors = make(map[string]VendorConfig)
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		vc = VendorConfig{Endpoints: make(map[string]EndpointConfig)}
		c.Vendors[vendor] = vc
		changed = true
	}
	if _, ok := vc.Endpoints[endpoint]; !ok {
		vc.Endpoints[endpoint] = EndpointConfig{}
		c.Vendors[vendor] = vc
		changed = true
	}
	return changed
}

func resolveEffectiveAPIKeyRef(epKey, vcKey string) string {
	epKey = strings.TrimSpace(epKey)
	vcKey = strings.TrimSpace(vcKey)

	if epKey == "" {
		return vcKey
	}

	if !envReferencePattern.MatchString(epKey) {
		return epKey
	}

	expanded := ExpandEnv(epKey)
	if expanded != epKey {

		return epKey
	}

	if vcKey != "" {
		return vcKey
	}

	return epKey
}
