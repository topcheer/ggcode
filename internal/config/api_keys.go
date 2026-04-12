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
	Vendor   string
	Endpoint string
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
				Vendor: vendorID,
				EnvVar: preferredVendorAPIKeyEnvVar(vendorID),
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
					EnvVar:   preferredEndpointAPIKeyEnvVar(vendorID, endpointID),
				})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Vendor != findings[j].Vendor {
			return findings[i].Vendor < findings[j].Vendor
		}
		return findings[i].Endpoint < findings[j].Endpoint
	})
	return findings
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
