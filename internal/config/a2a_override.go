package config

import (
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/debug"

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
		// #1515-E1: a parse error used to silently return nil - a typo'd
		// a2a.yaml disabled the WHOLE override file with zero signal and
		// the user kept the global config with no hint why. The main Load
		// path returns the error; mirror at least a debug log line.
		debug.Log("config", "a2a override %s: parse error (override ignored): %v", path, err)
		return nil
	}
	// #1515-A: the main config, vendors and IM sections all expand ${VAR}
	// references - the a2a override was the one config surface that did
	// not, so `auth: {api_key: ${A2A_KEY}}` merged the LITERAL
	// "${A2A_KEY}" string over the real value and auth failed silently.
	// Expand before the legacy-key migration below so both the flat
	// api_key form and the nested auth form resolve identically.
	raw = ExpandEnvRecursive(raw)
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
		debug.Log("config", "a2a override %s: re-marshal error (override ignored): %v", path, err)
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
	MergeA2AConfigWithGate(base, override, nil)
}

// MergeA2AConfigWithGate is MergeA2AConfig with an optional ownership gate.
// When gate(key) returns true for a field's dotted instance explicit-key path
// (e.g. "a2a.port"), that override field is skipped entirely so a value the
// instance config explicitly contains is never re-stomped by the legacy
// override file (r138: LoadWithInstance wires this to instanceCfg.explicitKeys,
// the #2284-C/#2702 mechanism). A nil gate applies every field unchanged.
func MergeA2AConfigWithGate(base *A2AConfig, override *A2AConfig, gate func(key string) bool) {
	if override == nil {
		return
	}
	gated := func(key string) bool { return gate != nil && gate(key) }
	// #665: instance wins — when the "disabled" key was explicitly present
	// in the override yaml (true OR false), assign it unconditionally so a
	// workspace a2a.yaml with `disabled: false` can re-enable a globally
	// disabled A2A. Overrides constructed programmatically (no explicit
	// marker) keep the legacy one-way merge: only disable, never re-enable.
	// r138: the instance-explicit gate outranks even the #665 re-enable -
	// a workspace that explicitly disabled A2A via its instance config must
	// not be silently re-enabled by a stale legacy file.
	if !gated("a2a.disabled") {
		if override.disabledExplicit {
			base.Disabled = override.Disabled
		} else if override.Disabled {
			base.Disabled = true
		}
	}
	if !gated("a2a.port") && override.Port != 0 {
		base.Port = override.Port
	}
	if !gated("a2a.host") && override.Host != "" {
		base.Host = override.Host
	}
	if !gated("a2a.max_tasks") && override.MaxTasks != 0 {
		base.MaxTasks = override.MaxTasks
	}
	if !gated("a2a.task_timeout") && override.TaskTimeout != "" {
		base.TaskTimeout = override.TaskTimeout
	}

	// Auth overrides
	if !gated("a2a.auth.api_key") && override.Auth.APIKey != "" {
		base.Auth.APIKey = override.Auth.APIKey
	}
	if !gated("a2a.auth.api_keys") && len(override.Auth.APIKeys) > 0 {
		base.Auth.APIKeys = append(base.Auth.APIKeys, override.Auth.APIKeys...)
	}
	if !gated("a2a.auth.oauth2") && override.Auth.OAuth2 != nil {
		base.Auth.OAuth2 = override.Auth.OAuth2
	}
	if !gated("a2a.auth.oidc") && override.Auth.OIDC != nil {
		base.Auth.OIDC = override.Auth.OIDC
	}
	if !gated("a2a.auth.mtls") && override.Auth.MTLS != nil {
		base.Auth.MTLS = override.Auth.MTLS
	}
}
