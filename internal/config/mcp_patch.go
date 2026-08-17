package config

// PatchMCPServerConfig merges a patch into a base MCPServerConfig and
// normalizes fields that are meaningless for the resulting server type
// (#584 M2): switching stdio→http must clear Command/Args, and
// http→stdio must clear URL/Headers, so stale transport fields cannot
// linger after a type switch.
func PatchMCPServerConfig(base, patch MCPServerConfig) MCPServerConfig {
	merged := base

	if patch.Name != "" {
		merged.Name = patch.Name
	}
	if patch.Type != "" {
		merged.Type = patch.Type
	}
	if patch.Command != "" {
		merged.Command = patch.Command
	}
	if len(patch.Args) > 0 {
		merged.Args = patch.Args
	}
	if patch.URL != "" {
		merged.URL = patch.URL
	}
	if patch.ReadOnly {
		merged.ReadOnly = true
	}
	if patch.OAuthClientID != "" {
		merged.OAuthClientID = patch.OAuthClientID
	}
	if patch.OAuthClientSecret != "" {
		merged.OAuthClientSecret = patch.OAuthClientSecret
	}

	// Maps: overlay patch entries onto a copy so the result does not
	// alias either input.
	merged.Env = mergeStringMap(base.Env, patch.Env)
	merged.Headers = mergeStringMap(base.Headers, patch.Headers)

	// Type normalization: drop fields that belong to the other transport.
	switch merged.Type {
	case "http", "https", "sse", "streamable-http":
		merged.Command = ""
		merged.Args = nil
	default: // "stdio", "command", "so", "grpc", ""
		merged.URL = ""
		merged.Headers = nil
	}
	return merged
}

// mergeStringMap returns a new map with base entries overlaid by patch
// entries. Nil inputs yield a nil result when both are empty.
func mergeStringMap(base, patch map[string]string) map[string]string {
	if len(base) == 0 && len(patch) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(patch))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		out[k] = v
	}
	return out
}
