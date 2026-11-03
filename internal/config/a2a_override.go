package config

import (
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"

	"gopkg.in/yaml.v3"
)

func LoadA2AOverride(workspace string) *A2AConfig {
	path := filepath.Join(workspace, ".ggcode", "a2a.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {

		debug.Log("config", "a2a override %s: parse error (override ignored): %v", path, err)
		return nil
	}

	raw = ExpandEnvRecursive(raw)

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
		debug.Log("config", "a2a override %s: re-marshal error (override ignored): %v", path, err)
		return nil
	}

	var override A2AConfig
	if err := yaml.Unmarshal(migrated, &override); err != nil {
		return nil
	}

	if _, ok := raw["disabled"]; ok {
		override.disabledExplicit = true
	}
	return &override
}

func MergeA2AConfig(base *A2AConfig, override *A2AConfig) {
	if override == nil {
		return
	}

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
