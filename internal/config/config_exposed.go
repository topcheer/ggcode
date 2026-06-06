package config

// Exported wrappers for functions needed by config tool in agentruntime package.
// These delegate to the unexported implementations in api_keys.go.

// IsEnvReference returns true if the value is an environment variable reference like ${VAR}.
func IsEnvReference(value string) (envVarName string, ok bool) {
	return envReferenceVarName(value)
}

// IsPlaintextSecret returns true if the value looks like a plaintext secret
// (non-empty and not an env reference).
func IsPlaintextSecret(value string) bool {
	return isPlaintextAPIKeyValue(value)
}

// LooksLikeSecretField returns true if the key name contains secret-like substrings.
func LooksLikeSecretField(key string) bool {
	return looksLikeSecretField(key)
}

// WriteKeysEnv persists key-value pairs to ~/.ggcode/keys.env (merging with existing).
func WriteKeysEnv(entries map[string]string) error {
	return writeKeysEnv(entries)
}

// IMAdapterSecretEnvVar returns the env var name for an IM adapter secret field.
func IMAdapterSecretEnvVar(adapterName, key string) string {
	return secretFieldEnvVar(adapterName, key)
}

// MCPServerEnvVar returns the env var name for an MCP server env entry.
func MCPServerEnvVar(srvName, key string) string {
	return mcpEnvVar(srvName, key)
}

// MCPServerHeaderEnvVar returns the env var name for an MCP server header entry.
func MCPServerHeaderEnvVar(srvName, key string) string {
	return mcpHeaderEnvVar(srvName, key)
}

// GetSaveScope returns the current save scope ("global" or "instance").
func (c *Config) GetSaveScope() string {
	if c == nil {
		return "global"
	}
	if c.saveScope == "" {
		return "global"
	}
	return c.saveScope
}

// A2ASecretEnvVar returns the env var name for an A2A secret.
func A2ASecretEnvVar(key string) string {
	return "GGCODE_A2A_" + sanitizeEnvVarSegment(key)
}
