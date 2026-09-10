package config

import (
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"os"
	"strings"
)

// SetEndpointAPIKey updates the active endpoint or vendor-level API key.
// The key is stored as an environment variable reference (e.g. ${ZAI_API_KEY})
// rather than plaintext, and the caller should set the actual value in the
// shell environment (os.Setenv) so the current session can use it immediately.
func (c *Config) SetEndpointAPIKey(vendor, endpoint, apiKey string, vendorScoped bool) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}

	apiKey = strings.TrimSpace(apiKey)

	// If the value is already an env reference (${VAR}), store as-is.
	if _, isRef := envReferenceVarName(apiKey); isRef || apiKey == "" {
		if vendorScoped {
			if apiKey == "" {
				clearAPIKeyRefEnv(vc.APIKey) // #1706 case 3
			}
			vc.APIKey = apiKey
			c.Vendors[vendor] = vc
		} else {
			ep, ok := vc.Endpoints[endpoint]
			if !ok {
				return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
			}
			if apiKey == "" {
				clearAPIKeyRefEnv(ep.APIKey) // #1706 case 3
			}
			ep.APIKey = apiKey
			vc.Endpoints[endpoint] = ep
			c.Vendors[vendor] = vc
		}
		return nil
	}

	// Plaintext key: resolve the preferred env var name and store the reference.
	var envVarName string
	if vendorScoped {
		envVarName = preferredVendorAPIKeyEnvVar(vendor)
	} else {
		envVarName = preferredEndpointAPIKeyEnvVar(vendor, endpoint)
	}

	// Set the actual value in the current process environment so it works
	// immediately for the current session.
	os.Setenv(envVarName, apiKey)

	// Persist to keys.env so the key survives restarts.
	// #1706 case 2: a failure here used to be swallowed (debug-only) -
	// the key lived in process env, vanished on restart, and NeedsOnboard
	// could re-trigger. SetVendorAPIKey already hard-errors on this exact
	// condition (#1517); same semantics now (os.Setenv above keeps the
	// current session working even when the caller logs and continues).
	if err := writeKeysEnv(map[string]string{envVarName: apiKey}); err != nil {
		return fmt.Errorf("persisting API key for %s/%s: %w", vendor, endpoint, err)
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

// clearAPIKeyRefEnv removes the keys.env entry backing a ${VAR} ref (if
// any) so a cleared key does not leave stale plaintext behind (#1706
// case 3).
func clearAPIKeyRefEnv(ref string) {
	if name, ok := envReferenceVarName(ref); ok {
		if err := removeKeysEnv([]string{name}); err != nil {
			debug.Log("config", "failed to remove %s from keys.env: %v", name, err)
		}
		os.Unsetenv(name)
	}
}

// SetVendorAPIKey sets the vendor-level API key.
func (c *Config) SetVendorAPIKey(vendor, apiKey string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q not found", vendor)
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		// #1706 case 3: drop the keys.env entry backing the old ref.
		clearAPIKeyRefEnv(vc.APIKey)
		vc.APIKey = ""
	} else if _, isRef := envReferenceVarName(apiKey); isRef {
		vc.APIKey = apiKey
	} else {
		envVarName := preferredEndpointAPIKeyEnvVar(vendor, "default")
		os.Setenv(envVarName, apiKey)
		// #1517: #1435 fixed this exact class for AddVendor/AddEndpoint
		// but missed SetVendorAPIKey - the key lived only in process env,
		// worked until restart, then NeedsOnboard forced re-onboarding on
		// every fresh terminal. Persist like the siblings.
		if err := writeKeysEnv(map[string]string{envVarName: apiKey}); err != nil {
			return fmt.Errorf("persisting API key for vendor %s: %w", vendor, err)
		}
		vc.APIKey = "${" + envVarName + "}"
	}
	c.Vendors[vendor] = vc
	return nil
}

// resolveEffectiveAPIKeyRef returns the raw API key reference string.
// Endpoint key takes priority, but if it's an unresolvable ${VAR} reference
// (the environment variable is not set), falls back to the vendor key.
// The returned value may still contain ${VAR} references — expansion is the
// caller's responsibility.
func resolveEffectiveAPIKeyRef(epKey, vcKey string) string {
	epKey = strings.TrimSpace(epKey)
	vcKey = strings.TrimSpace(vcKey)

	// If endpoint key is empty, use vendor key directly
	if epKey == "" {
		return vcKey
	}

	// If endpoint key is not a ${VAR} reference, use it directly
	if !envReferencePattern.MatchString(epKey) {
		return epKey
	}

	// Endpoint key is a ${VAR} reference — check if it resolves
	expanded := ExpandEnv(epKey)
	if expanded != epKey {
		// Resolved successfully (env var exists)
		return epKey
	}

	// Unresolvable ${VAR} — fall back to vendor key if available
	if vcKey != "" {
		return vcKey
	}

	return epKey
}
