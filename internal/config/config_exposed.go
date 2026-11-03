package config

func IsEnvReference(value string) (envVarName string, ok bool) {
	return envReferenceVarName(value)
}

func IsPlaintextSecret(value string) bool {
	return isPlaintextAPIKeyValue(value)
}

func LooksLikeSecretField(key string) bool {
	return looksLikeSecretField(key)
}

func WriteKeysEnv(entries map[string]string) error {
	return writeKeysEnv(entries)
}

func IMAdapterSecretEnvVar(adapterName, key string) string {
	return secretFieldEnvVar(adapterName, key)
}

func MCPServerEnvVar(srvName, key string) string {
	return mcpEnvVar(srvName, key)
}

func MCPServerHeaderEnvVar(srvName, key string) string {
	return mcpHeaderEnvVar(srvName, key)
}

func (c *Config) GetSaveScope() string {
	if c == nil {
		return "global"
	}
	if c.saveScope == "" {
		return "global"
	}
	return c.saveScope
}

func A2ASecretEnvVar(key string) string {
	return "GGCODE_A2A_" + sanitizeEnvVarSegment(key)
}
