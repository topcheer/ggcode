package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadA2AOverride loads instance-level A2A config from .ggcode/a2a.yaml
// in the given workspace directory. Returns nil if no override file exists.
// Fields set here override the corresponding fields from the global config.
func LoadA2AOverride(workspace string) *A2AConfig {
	path := filepath.Join(workspace, ".ggcode", "a2a.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil
	}
	// a2a.yaml override files are flat (no "a2a:" wrapper), so check
	// top-level api_key and move it to auth.api_key directly.
	if legacyKey, hasLegacy := raw["api_key"]; hasLegacy {
		auth, _ := raw["auth"].(map[string]interface{})
		if auth == nil {
			auth = map[string]interface{}{}
		}
		if _, exists := auth["api_key"]; !exists {
			auth["api_key"] = legacyKey
			raw["auth"] = auth
		}
		delete(raw, "api_key")
	}

	migrated, err := yaml.Marshal(raw)
	if err != nil {
		return nil
	}

	var override A2AConfig
	if err := yaml.Unmarshal(migrated, &override); err != nil {
		return nil
	}
	// #665: remember whether the "disabled" key was explicitly present in
	// the source yaml (either true or false). MergeA2AConfig uses this to
	// distinguish "explicitly set" from "absent" (zero value), enabling
	// the instance-wins semantics in both directions.
	if _, ok := raw["disabled"]; ok {
		override.disabledExplicit = true
	}
	return &override
}

// MergeA2AConfig applies instance-level overrides on top of global A2A config.
// Only non-zero fields from override are applied.
func MergeA2AConfig(base *A2AConfig, override *A2AConfig) {
	if override == nil {
		return
	}
	// #665: instance wins — when the "disabled" key was explicitly present
	// in the override yaml (true OR false), assign it unconditionally so a
	// workspace a2a.yaml with `disabled: false` can re-enable a globally
	// disabled A2A. Overrides constructed programmatically (no explicit
	// marker) keep the legacy one-way merge: only disable, never re-enable.
	if override.disabledExplicit {
		base.Disabled = override.Disabled
	} else if override.Disabled {
		base.Disabled = true
	}
	if override.Port != 0 {
		base.Port = override.Port
	}
	if override.Host != "" {
		base.Host = override.Host
	}
	if override.MaxTasks != 0 {
		base.MaxTasks = override.MaxTasks
	}
	if override.TaskTimeout != "" {
		base.TaskTimeout = override.TaskTimeout
	}

	// Auth overrides
	if override.Auth.APIKey != "" {
		base.Auth.APIKey = override.Auth.APIKey
	}
	if len(override.Auth.APIKeys) > 0 {
		base.Auth.APIKeys = append(base.Auth.APIKeys, override.Auth.APIKeys...)
	}
	if override.Auth.OAuth2 != nil {
		base.Auth.OAuth2 = override.Auth.OAuth2
	}
	if override.Auth.OIDC != nil {
		base.Auth.OIDC = override.Auth.OIDC
	}
	if override.Auth.MTLS != nil {
		base.Auth.MTLS = override.Auth.MTLS
	}
}
