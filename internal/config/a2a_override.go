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

	var override A2AConfig
	if err := yaml.Unmarshal(data, &override); err != nil {
		return nil
	}
	return &override
}

// MergeA2AConfig applies instance-level overrides on top of global A2A config.
// Only non-zero fields from override are applied.
func MergeA2AConfig(base *A2AConfig, override *A2AConfig) {
	if override == nil {
		return
	}
	if override.Disabled {
		base.Disabled = true
	}
	if override.Port != 0 {
		base.Port = override.Port
	}
	if override.Host != "" {
		base.Host = override.Host
	}
	if override.APIKey != "" {
		base.APIKey = override.APIKey
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
