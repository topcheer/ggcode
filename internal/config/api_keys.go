package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type APIKeyFinding struct {
	Vendor   string // vendor name (for section="vendor")
	Endpoint string // endpoint name (for section="vendor")
	Section  string // "vendor", "im", "mcp_env", "mcp_headers"
	KeyPath  string // dot-separated path for non-vendor findings (e.g. "im.adapters.ggcode.extra.token")
	EnvVar   string
}

var envReferencePattern = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

var preferredVendorAPIKeyEnvVars = map[string]string{
	"aliyun":     "DASHSCOPE_API_KEY",
	"aihubmix":   "AIHUBMIX_API_KEY",
	"anthropic":  "ANTHROPIC_API_KEY",
	"ark":        "ARK_API_KEY",
	"deepseek":   "DEEPSEEK_API_KEY",
	"gemini":     "GEMINI_API_KEY",
	"getgoapi":   "GETGOAPI_API_KEY",
	"google":     "GEMINI_API_KEY",
	"groq":       "GROQ_API_KEY",
	"kimi":       "KIMI_API_KEY",
	"minimax":    "MINIMAX_API_KEY",
	"mistral":    "MISTRAL_API_KEY",
	"moonshot":   "MOONSHOT_API_KEY",
	"novita":     "NOVITA_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"perplexity": "PERPLEXITY_API_KEY",
	"poe":        "POE_API_KEY",
	"requesty":   "REQUESTY_API_KEY",
	"together":   "TOGETHER_API_KEY",
	"vercel":     "VERCEL_AI_GATEWAY_API_KEY",
	"zai":        "ZAI_API_KEY",
}

func DetectPlaintextAPIKeys(path string) ([]APIKeyFinding, error) {
	raw, err := loadRawConfigMap(path)
	if err != nil {
		return nil, err
	}
	return detectPlaintextAPIKeysFromRaw(raw), nil
}

func loadRawConfigMap(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{}, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	raw := map[string]interface{}{}
	if len(data) == 0 {
		return raw, nil
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return raw, nil
}

func detectPlaintextAPIKeysFromRaw(raw map[string]interface{}) []APIKeyFinding {
	vendors, _ := raw["vendors"].(map[string]interface{})
	findings := make([]APIKeyFinding, 0)
	for vendorID, vendorValue := range vendors {
		vendorMap, ok := vendorValue.(map[string]interface{})
		if !ok {
			continue
		}
		if value, ok := stringValue(vendorMap["api_key"]); ok && isPlaintextAPIKeyValue(value) {
			findings = append(findings, APIKeyFinding{
				Vendor:  vendorID,
				Section: "vendor",
				EnvVar:  preferredVendorAPIKeyEnvVar(vendorID),
			})
		}
		endpoints, _ := vendorMap["endpoints"].(map[string]interface{})
		for endpointID, endpointValue := range endpoints {
			endpointMap, ok := endpointValue.(map[string]interface{})
			if !ok {
				continue
			}
			if value, ok := stringValue(endpointMap["api_key"]); ok && isPlaintextAPIKeyValue(value) {
				findings = append(findings, APIKeyFinding{
					Vendor:   vendorID,
					Endpoint: endpointID,
					Section:  "vendor",
					EnvVar:   preferredEndpointAPIKeyEnvVar(vendorID, endpointID),
				})
			}
		}
	}
	// --- Scan IM adapters: extra fields matching secret/token/password/credential ---
	im, _ := raw["im"].(map[string]interface{})
	adapters, _ := im["adapters"].(map[string]interface{})
	for adapterName, adapterValue := range adapters {
		adapterMap, ok := adapterValue.(map[string]interface{})
		if !ok {
			continue
		}
		extra, _ := adapterMap["extra"].(map[string]interface{})
		for key, val := range extra {
			s, ok := val.(string)
			if !ok || !isPlaintextAPIKeyValue(s) {
				continue
			}
			if !looksLikeSecretField(key) {
				continue
			}
			keyPath := "im.adapters." + adapterName + ".extra." + key
			envVar := secretFieldEnvVar(adapterName, key)
			findings = append(findings, APIKeyFinding{
				Section: "im",
				KeyPath: keyPath,
				EnvVar:  envVar,
			})
		}
	}

	// --- Scan MCP servers: env block (all values are secrets) ---
	mcpServers, _ := raw["mcp_servers"].([]interface{})
	for i, srvVal := range mcpServers {
		srv, ok := srvVal.(map[string]interface{})
		if !ok {
			continue
		}
		srvName, _ := srv["name"].(string)
		if srvName == "" {
			srvName = fmt.Sprintf("mcp_%d", i)
		}
		// env block: all string values are secrets
		env, _ := srv["env"].(map[string]interface{})
		for key, val := range env {
			s, ok := val.(string)
			if !ok || !isPlaintextAPIKeyValue(s) {
				continue
			}
			keyPath := fmt.Sprintf("mcp_servers[%s].env.%s", srvName, key)
			envVar := mcpEnvVar(srvName, key)
			findings = append(findings, APIKeyFinding{
				Section: "mcp_env",
				KeyPath: keyPath,
				EnvVar:  envVar,
			})
		}
		// headers block: all string values are secrets
		headers, _ := srv["headers"].(map[string]interface{})
		for key, val := range headers {
			s, ok := val.(string)
			if !ok || !isPlaintextAPIKeyValue(s) {
				continue
			}
			keyPath := fmt.Sprintf("mcp_servers[%s].headers.%s", srvName, key)
			envVar := mcpHeaderEnvVar(srvName, key)
			findings = append(findings, APIKeyFinding{
				Section: "mcp_headers",
				KeyPath: keyPath,
				EnvVar:  envVar,
			})
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		return findings[i].KeyPath < findings[j].KeyPath
	})
	return findings
}

// looksLikeSecretField returns true if the key name suggests it holds a secret.
// Matches: secret, token, password, credential (case-insensitive, as substring).
func looksLikeSecretField(key string) bool {
	lower := strings.ToLower(key)
	for _, pattern := range []string{"secret", "token", "password", "credential"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// secretFieldEnvVar builds an env var name for an IM adapter secret field.
func secretFieldEnvVar(adapterName, key string) string {
	return "GGCODE_IM_" + sanitizeEnvVarSegment(adapterName) + "_" + sanitizeEnvVarSegment(key)
}

// mcpEnvVar builds an env var name for an MCP server env entry.
func mcpEnvVar(srvName, key string) string {
	return "GGCODE_MCP_" + sanitizeEnvVarSegment(srvName) + "_" + sanitizeEnvVarSegment(key)
}

// mcpHeaderEnvVar builds an env var name for an MCP server header entry.
func mcpHeaderEnvVar(srvName, key string) string {
	return "GGCODE_MCP_" + sanitizeEnvVarSegment(srvName) + "_HEADER_" + sanitizeEnvVarSegment(key)
}

func preferredVendorAPIKeyEnvVar(vendor string) string {
	if envVar, ok := preferredVendorAPIKeyEnvVars[strings.ToLower(strings.TrimSpace(vendor))]; ok {
		return envVar
	}
	return sanitizeEnvVarSegment(vendor) + "_API_KEY"
}

func PreferredVendorAPIKeyEnvVar(vendor string) string {
	return preferredVendorAPIKeyEnvVar(vendor)
}

func preferredEndpointAPIKeyEnvVar(vendor, endpoint string) string {
	return sanitizeEnvVarSegment(vendor) + "_" + sanitizeEnvVarSegment(endpoint) + "_API_KEY"
}

func PreferredEndpointAPIKeyEnvVar(vendor, endpoint string) string {
	return preferredEndpointAPIKeyEnvVar(vendor, endpoint)
}

func apiKeyEnvVarForValue(vendor, endpoint, value string) string {
	if envVar, ok := envReferenceVarName(value); ok {
		return envVar
	}
	if strings.TrimSpace(endpoint) != "" {
		return preferredEndpointAPIKeyEnvVar(vendor, endpoint)
	}
	return preferredVendorAPIKeyEnvVar(vendor)
}

func PreferredAPIKeyEnvVar(vendor, endpoint string, vendorAPIKey, endpointAPIKey string) string {
	if strings.TrimSpace(endpointAPIKey) != "" {
		return apiKeyEnvVarForValue(vendor, endpoint, endpointAPIKey)
	}
	if strings.TrimSpace(vendorAPIKey) != "" {
		return apiKeyEnvVarForValue(vendor, "", vendorAPIKey)
	}
	return preferredVendorAPIKeyEnvVar(vendor)
}

func envReferenceVarName(value string) (string, bool) {
	matches := envReferencePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 2 {
		return "", false
	}
	return matches[1], true
}

func isPlaintextAPIKeyValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if _, ok := envReferenceVarName(value); ok {
		return false
	}
	return true
}

func stringValue(value interface{}) (string, bool) {
	s, ok := value.(string)
	return s, ok
}

func sanitizeEnvVarSegment(value string) string {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return "GGCODE"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if lastUnderscore {
			continue
		}
		b.WriteByte('_')
		lastUnderscore = true
	}
	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "GGCODE"
	}
	return result
}

type plaintextAPIKeyWarningState struct {
	IgnoredConfigPaths []string `json:"ignored_config_paths"`
}

func IsPlaintextAPIKeyWarningIgnored(configPath string) (bool, error) {
	state, err := loadPlaintextAPIKeyWarningState()
	if err != nil {
		return false, err
	}
	normalized := normalizeWarningConfigPath(configPath)
	for _, path := range state.IgnoredConfigPaths {
		if path == normalized {
			return true, nil
		}
	}
	return false, nil
}

func IgnorePlaintextAPIKeyWarning(configPath string) error {
	state, err := loadPlaintextAPIKeyWarningState()
	if err != nil {
		return err
	}
	normalized := normalizeWarningConfigPath(configPath)
	for _, path := range state.IgnoredConfigPaths {
		if path == normalized {
			return nil
		}
	}
	state.IgnoredConfigPaths = append(state.IgnoredConfigPaths, normalized)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling plaintext api key warning state: %w", err)
	}
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(plaintextAPIKeyWarningStatePath(), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing plaintext api key warning state: %w", err)
	}
	return nil
}

func loadPlaintextAPIKeyWarningState() (*plaintextAPIKeyWarningState, error) {
	path := plaintextAPIKeyWarningStatePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &plaintextAPIKeyWarningState{}, nil
		}
		return nil, fmt.Errorf("reading plaintext api key warning state: %w", err)
	}
	state := &plaintextAPIKeyWarningState{}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("parsing plaintext api key warning state: %w", err)
	}
	return state, nil
}

func plaintextAPIKeyWarningStatePath() string {
	return filepath.Join(ConfigDir(), "plaintext_api_key_warning.json")
}

func normalizeWarningConfigPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// MigratePlaintextAPIKeys detects plaintext API keys in the config file,
// sets them as environment variables for the current process, persists them
// to ~/.ggcode/keys.env for future sessions, and rewrites the YAML to use
// ${VAR} references. Returns the list of migrated keys so callers can log
// the migration. If no plaintext keys are found, it returns an empty slice
// and nil error without touching any file.
func MigratePlaintextAPIKeys(path string) ([]APIKeyFinding, error) {
	raw, err := loadRawConfigMap(path)
	if err != nil {
		return nil, err
	}
	if raw == nil || len(raw) == 0 {
		return nil, nil
	}

	findings := detectPlaintextAPIKeysFromRaw(raw)
	if len(findings) == 0 {
		return nil, nil
	}

	// Collect key=value pairs for keys.env.
	envEntries := make(map[string]string)

	for _, f := range findings {
		switch f.Section {
		case "vendor":
			migrateVendorFinding(raw, f, envEntries)
		case "im":
			migrateIMFinding(raw, f, envEntries)
		case "mcp_env", "mcp_headers":
			migrateMCPFinding(raw, f, envEntries)
		}
	}

	// Persist to ~/.ggcode/keys.env (merge with existing entries).
	if err := writeKeysEnv(envEntries); err != nil {
		return nil, fmt.Errorf("writing keys.env: %w", err)
	}

	// Rewrite the config YAML with ${VAR} references.
	updated, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshaling migrated config: %w", err)
	}
	if err := os.WriteFile(path, updated, 0600); err != nil {
		return nil, fmt.Errorf("writing migrated config %s: %w", path, err)
	}
	// Force exact permissions (os.WriteFile respects umask).
	_ = os.Chmod(path, 0600)

	return findings, nil
}

// KeysEnvPath returns the path to the managed env file for API keys.
func KeysEnvPath() string {
	if keysEnvPathOverride != "" {
		return keysEnvPathOverride
	}
	return filepath.Join(ConfigDir(), "keys.env")
}

// keysEnvPathOverride is set by tests to redirect keys.env to a temp directory.
var keysEnvPathOverride string

// LoadKeysEnv loads API keys from ~/.ggcode/keys.env into the current
// process environment. Keys that are already set in the environment take
// precedence and are not overwritten.
func LoadKeysEnv() error {
	return loadKeysEnvInto(os.Setenv)
}

func loadKeysEnvInto(setenv func(string, string) error) error {
	path := KeysEnvPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading keys.env: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := parseEnvAssignment(line)
		if !ok {
			continue
		}
		// Do not overwrite existing env vars — user's shell env takes precedence.
		if _, exists := os.LookupEnv(name); exists {
			continue
		}
		_ = setenv(name, value)
	}
	return nil
}

func writeKeysEnv(newEntries map[string]string) error {
	path := KeysEnvPath()

	// Load existing entries first so we merge rather than overwrite.
	existing := make(map[string]string)
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			name, value, ok := parseEnvAssignment(line)
			if ok {
				existing[name] = value
			}
		}
	}

	// Merge new entries.
	for k, v := range newEntries {
		existing[k] = v
	}

	// Write deterministic output (sorted by key).
	keys := make([]string, 0, len(existing))
	for k := range existing {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("# Managed by ggcode — DO NOT EDIT manually.\n")
	b.WriteString("# Use ggcode config to change API keys.\n")
	for _, k := range keys {
		// Use single quotes to avoid shell expansion issues.
		fmt.Fprintf(&b, "export %s='%s'\n", k, existing[k])
	}

	if err := os.MkdirAll(ConfigDir(), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	// 0600: only owner can read/write — these are secrets.
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		return fmt.Errorf("writing keys.env: %w", err)
	}
	// Force exact permissions (os.WriteFile respects umask).
	_ = os.Chmod(path, 0600)
	return nil
}

// migrateVendorFinding handles migration for vendors.{name}.api_key and
// vendors.{name}.endpoints.{ep}.api_key.
func migrateVendorFinding(raw map[string]interface{}, f APIKeyFinding, envEntries map[string]string) {
	vendors, ok := raw["vendors"].(map[string]interface{})
	if !ok {
		return
	}
	vendorMap, ok := vendors[f.Vendor].(map[string]interface{})
	if !ok {
		return
	}
	if f.Endpoint == "" {
		value, ok := stringValue(vendorMap["api_key"])
		if !ok || !isPlaintextAPIKeyValue(value) {
			return
		}
		os.Setenv(f.EnvVar, value)
		envEntries[f.EnvVar] = value
		vendorMap["api_key"] = "${" + f.EnvVar + "}"
	} else {
		endpoints, _ := vendorMap["endpoints"].(map[string]interface{})
		epMap, ok := endpoints[f.Endpoint].(map[string]interface{})
		if !ok {
			return
		}
		value, ok := stringValue(epMap["api_key"])
		if !ok || !isPlaintextAPIKeyValue(value) {
			return
		}
		os.Setenv(f.EnvVar, value)
		envEntries[f.EnvVar] = value
		epMap["api_key"] = "${" + f.EnvVar + "}"
		endpoints[f.Endpoint] = epMap
	}
	vendors[f.Vendor] = vendorMap
}

// migrateIMFinding handles migration for im.adapters.{name}.extra.{field}.
func migrateIMFinding(raw map[string]interface{}, f APIKeyFinding, envEntries map[string]string) {
	im, _ := raw["im"].(map[string]interface{})
	adapters, _ := im["adapters"].(map[string]interface{})
	// KeyPath format: "im.adapters.{adapterName}.extra.{key}"
	parts := strings.SplitN(f.KeyPath, ".", 5)
	if len(parts) < 5 {
		return
	}
	adapterName := parts[2]
	keyName := parts[4]
	adapterMap, ok := adapters[adapterName].(map[string]interface{})
	if !ok {
		return
	}
	extra, _ := adapterMap["extra"].(map[string]interface{})
	value, ok := stringValue(extra[keyName])
	if !ok || !isPlaintextAPIKeyValue(value) {
		return
	}
	os.Setenv(f.EnvVar, value)
	envEntries[f.EnvVar] = value
	extra[keyName] = "${" + f.EnvVar + "}"
	adapterMap["extra"] = extra
	adapters[adapterName] = adapterMap
}

// migrateMCPFinding handles migration for mcp_servers[].env.* and mcp_servers[].headers.*.
func migrateMCPFinding(raw map[string]interface{}, f APIKeyFinding, envEntries map[string]string) {
	mcpServers, _ := raw["mcp_servers"].([]interface{})
	// KeyPath format: "mcp_servers[{srvName}].env.{key}" or "mcp_servers[{srvName}].headers.{key}"
	// Extract server name between [ and ]
	start := strings.Index(f.KeyPath, "[")
	end := strings.Index(f.KeyPath, "]")
	if start < 0 || end < 0 || end <= start+1 {
		return
	}
	srvName := f.KeyPath[start+1 : end]
	// Determine if env or headers
	isHeader := strings.Contains(f.KeyPath, ".headers.")
	var keyName string
	if isHeader {
		parts := strings.SplitN(f.KeyPath, ".headers.", 2)
		if len(parts) < 2 {
			return
		}
		keyName = parts[1]
	} else {
		parts := strings.SplitN(f.KeyPath, ".env.", 2)
		if len(parts) < 2 {
			return
		}
		keyName = parts[1]
	}

	for i, srvVal := range mcpServers {
		srv, ok := srvVal.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := srv["name"].(string)
		if name != srvName {
			continue
		}
		if isHeader {
			headers, _ := srv["headers"].(map[string]interface{})
			value, ok := stringValue(headers[keyName])
			if !ok || !isPlaintextAPIKeyValue(value) {
				return
			}
			os.Setenv(f.EnvVar, value)
			envEntries[f.EnvVar] = value
			headers[keyName] = "${" + f.EnvVar + "}"
			srv["headers"] = headers
		} else {
			env, _ := srv["env"].(map[string]interface{})
			value, ok := stringValue(env[keyName])
			if !ok || !isPlaintextAPIKeyValue(value) {
				return
			}
			os.Setenv(f.EnvVar, value)
			envEntries[f.EnvVar] = value
			env[keyName] = "${" + f.EnvVar + "}"
			srv["env"] = env
		}
		mcpServers[i] = srv
		break
	}
}
