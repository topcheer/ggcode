package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/auth"
)

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name       string
		basePrompt string
		language   string
		workingDir string
		toolNames  []string
		gitStatus  string
		customCmds []string
		want       []string // substrings that must appear
	}{
		{
			name:       "default prompt",
			basePrompt: "",
			language:   "en",
			workingDir: "/tmp",
			toolNames:  []string{"read_file", "write_file"},
			want:       []string{"ggcode", "read_file", "write_file", "/tmp"},
		},
		{
			name:       "custom prompt",
			basePrompt: "You are a helper.",
			language:   "en",
			workingDir: "/home/user",
			toolNames:  []string{"bash"},
			want:       []string{"helper", "bash", "/home/user"},
		},
		{
			name:       "with git status",
			basePrompt: "",
			language:   "en",
			workingDir: "/tmp",
			toolNames:  []string{"git_status"},
			gitStatus:  "main, 2 commits ahead",
			want:       []string{"main", "2 commits ahead"},
		},
		{
			name:       "with custom commands",
			basePrompt: "",
			language:   "en",
			workingDir: "/tmp",
			toolNames:  []string{"bash"},
			customCmds: []string{"/deploy", "/build"},
			want:       []string{"/deploy", "/build"},
		},
		{
			name:       "with zh-CN reply guidance",
			basePrompt: "",
			language:   "zh-CN",
			workingDir: "/tmp",
			toolNames:  []string{"bash"},
			want:       []string{"Default to Simplified Chinese", "follow the user's current request for that turn"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSystemPrompt(tt.basePrompt, tt.workingDir, tt.language, tt.toolNames, tt.gitStatus, tt.customCmds)
			for _, substr := range tt.want {
				if !contains(result, substr) {
					t.Errorf("BuildSystemPrompt() missing %q in output", substr)
				}
			}
		})
	}
}

func TestLoad_NonExistent(t *testing.T) {
	cfg, err := Load("/nonexistent/path/ggcode.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}
	if cfg.SystemPrompt != DefaultSystemPrompt {
		t.Errorf("expected default system prompt, got %q", cfg.SystemPrompt)
	}
	if !cfg.FirstRun {
		t.Fatal("expected missing config to be marked as first run")
	}
}

func TestLoad_NonExistentExpandsEnvDefaults(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "test-key")
	cfg, err := Load("/nonexistent/path/ggcode.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Vendors["zai"].APIKey; got != "test-key" {
		t.Fatalf("expected expanded API key, got %q", got)
	}
}

func TestLoad_ExpandsEnvFromShellFilesWhenProcessEnvMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	original, hadOriginal := os.LookupEnv("ZAI_API_KEY")
	if err := os.Unsetenv("ZAI_API_KEY"); err != nil {
		t.Fatalf("Unsetenv() error = %v", err)
	}
	defer func() {
		if hadOriginal {
			_ = os.Setenv("ZAI_API_KEY", original)
			return
		}
		_ = os.Unsetenv("ZAI_API_KEY")
	}()
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo
vendors:
  zai:
    api_key: ${ZAI_API_KEY}
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("export ZAI_API_KEY='shell-value'\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(.zshrc) error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Vendors["zai"].APIKey; got != "shell-value" {
		t.Fatalf("expected shell fallback api key, got %q", got)
	}
}

func TestDetectPlaintextAPIKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	content := `
vendors:
  zai:
    api_key: real-zai
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
  openrouter:
    api_key: ${OPENROUTER_API_KEY}
    endpoints:
      api:
        protocol: openai
        base_url: https://example.com
  anthropic:
    endpoints:
      api:
        protocol: anthropic
        base_url: https://example.com
        api_key: endpoint-secret
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	findings, err := DetectPlaintextAPIKeys(path)
	if err != nil {
		t.Fatalf("DetectPlaintextAPIKeys() error = %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 plaintext findings, got %#v", findings)
	}
	if findings[0].Vendor != "anthropic" || findings[0].Endpoint != "api" || findings[0].EnvVar != "ANTHROPIC_API_API_KEY" {
		t.Fatalf("unexpected endpoint finding %#v", findings[0])
	}
	if findings[1].Vendor != "zai" || findings[1].EnvVar != "ZAI_API_KEY" {
		t.Fatalf("unexpected vendor finding %#v", findings[1])
	}
}

func TestPlaintextAPIKeyWarningIgnoreState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	ignored, err := IsPlaintextAPIKeyWarningIgnored(path)
	if err != nil {
		t.Fatalf("IsPlaintextAPIKeyWarningIgnored() error = %v", err)
	}
	if ignored {
		t.Fatal("expected no ignore state before persisting")
	}
	if err := IgnorePlaintextAPIKeyWarning(path); err != nil {
		t.Fatalf("IgnorePlaintextAPIKeyWarning() error = %v", err)
	}
	ignored, err = IsPlaintextAPIKeyWarningIgnored(path)
	if err != nil {
		t.Fatalf("IsPlaintextAPIKeyWarningIgnored() error = %v", err)
	}
	if !ignored {
		t.Fatal("expected config path to be ignored after persisting state")
	}
}

func TestLoad_NonExistentBootstrapsAnthropicVendorFromEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_BASE_URL", "https://router.requesty.ai/")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")

	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	settings := `{"env":{"ANTHROPIC_MODEL":"anthropic/claude-sonnet-4-5"}}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load("/nonexistent/path/ggcode.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Vendor != "requesty" {
		t.Fatalf("expected bootstrapped vendor requesty, got %q", cfg.Vendor)
	}
	if cfg.Endpoint != "env-anthropic" {
		t.Fatalf("expected bootstrapped endpoint env-anthropic, got %q", cfg.Endpoint)
	}
	if cfg.Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("expected model from Claude settings, got %q", cfg.Model)
	}

	vendor := cfg.Vendors["requesty"]
	if vendor.DisplayName != "Requesty" {
		t.Fatalf("expected display name Requesty, got %q", vendor.DisplayName)
	}
	ep := vendor.Endpoints["env-anthropic"]
	if ep.Protocol != "anthropic" {
		t.Fatalf("expected anthropic protocol, got %q", ep.Protocol)
	}
	if ep.BaseURL != "https://router.requesty.ai" {
		t.Fatalf("expected normalized base url, got %q", ep.BaseURL)
	}
	if ep.APIKey != "auth-token" {
		t.Fatalf("expected auth token to be used, got %q", ep.APIKey)
	}
}

func TestLoad_NonExistentBootstrapsAnthropicVendorPrefersAuthToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.anthropic.com")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")
	t.Setenv("ANTHROPIC_API_KEY", "api-key")

	cfg, err := Load("/nonexistent/path/ggcode.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Vendor != "anthropic" {
		t.Fatalf("expected existing anthropic vendor to be reused, got %q", cfg.Vendor)
	}
	if got := cfg.Vendors["anthropic"].Endpoints["api"].APIKey; got != "auth-token" {
		t.Fatalf("expected auth token to override api key, got %q", got)
	}
}

func TestLoad_NonExistentBootstrapsAnthropicVendorDefaultsModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_BASE_URL", "https://api.example.ai")
	t.Setenv("ANTHROPIC_API_KEY", "api-key")

	cfg, err := Load("/nonexistent/path/ggcode.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Model != defaultBootstrapAnthropicModel {
		t.Fatalf("expected default bootstrap model %q, got %q", defaultBootstrapAnthropicModel, cfg.Model)
	}
	if got := cfg.Vendors["example"].Endpoints["env-anthropic"].DefaultModel; got != defaultBootstrapAnthropicModel {
		t.Fatalf("expected endpoint default model %q, got %q", defaultBootstrapAnthropicModel, got)
	}
}

func TestLoad_ExistingLanguageOnlyFileStillBootstrapsAnthropicVendor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_BASE_URL", "https://router.requesty.ai")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")

	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	if err := os.WriteFile(path, []byte("language: zh-CN\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.FirstRun {
		t.Fatal("expected existing config file not to be marked as first run")
	}
	if cfg.Language != "zh-CN" {
		t.Fatalf("expected persisted language zh-CN, got %q", cfg.Language)
	}
	if cfg.Vendor != "requesty" {
		t.Fatalf("expected requesty bootstrap vendor, got %q", cfg.Vendor)
	}
}

func TestSaveLanguagePreferenceCreatesMinimalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := DefaultConfig()
	cfg.FilePath = path
	cfg.FirstRun = true

	if err := cfg.SaveLanguagePreference("zh-CN"); err != nil {
		t.Fatalf("SaveLanguagePreference() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "language: zh-CN\n" {
		t.Fatalf("expected minimal language config, got %q", got)
	}
	if cfg.Language != "zh-CN" {
		t.Fatalf("expected config language to update, got %q", cfg.Language)
	}
	if cfg.FirstRun {
		t.Fatal("expected SaveLanguagePreference to clear first-run flag")
	}
}

func TestSaveSidebarPreferenceCreatesUIConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := DefaultConfig()
	cfg.FilePath = path

	if err := cfg.SaveSidebarPreference(false); err != nil {
		t.Fatalf("SaveSidebarPreference() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.SidebarVisible() {
		t.Fatal("expected persisted sidebar preference to be false")
	}
	if cfg.SidebarVisible() {
		t.Fatal("expected in-memory sidebar preference to update")
	}
}

func TestSaveDefaultModePreferenceCreatesMinimalConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := DefaultConfig()
	cfg.FilePath = path

	if err := cfg.SaveDefaultModePreference("auto"); err != nil {
		t.Fatalf("SaveDefaultModePreference() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "default_mode: auto\n" {
		t.Fatalf("expected minimal default_mode config, got %q", got)
	}
	if cfg.DefaultMode != "auto" {
		t.Fatalf("expected in-memory default mode to update, got %q", cfg.DefaultMode)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	os.WriteFile(path, []byte(":\n  - invalid: ["), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
system_prompt: "Custom prompt"
allowed_dirs:
  - /tmp
`
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SystemPrompt != "Custom prompt" {
		t.Errorf("expected 'Custom prompt', got %q", cfg.SystemPrompt)
	}
	if len(cfg.AllowedDirs) != 1 || cfg.AllowedDirs[0] != "/tmp" {
		t.Errorf("expected [/tmp], got %v", cfg.AllowedDirs)
	}
}

func TestLoad_LegacyProviderRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
provider: unknown
model: test
providers:
  anthropic:
    api_key: key
`
	os.WriteFile(path, []byte(content), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for legacy provider config")
	}
}

func TestLoad_InvalidMaxIterations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: test
max_iterations: -1
vendors:
  zai:
    api_key: key
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	os.WriteFile(path, []byte(content), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for max_iterations")
	}
}

func TestLoad_ZeroMaxIterationsMeansUnlimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: test
max_iterations: 0
vendors:
  zai:
    api_key: key
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxIterations != 0 {
		t.Fatalf("expected max_iterations 0 to be preserved, got %d", cfg.MaxIterations)
	}
}

func TestLoad_DefaultUserConfigMigratesLegacyMaxIterations50ToUnlimited(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".ggcode")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(configDir, "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: test
max_iterations: 50
vendors:
  zai:
    api_key: key
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(ConfigPath())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxIterations != 0 {
		t.Fatalf("expected legacy max_iterations 50 in default user config to migrate to 0, got %d", cfg.MaxIterations)
	}
}

func TestLoad_ProjectConfigPreservesExplicitMaxIterations50(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: test
max_iterations: 50
vendors:
  zai:
    api_key: key
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxIterations != 50 {
		t.Fatalf("expected explicit project max_iterations 50 to be preserved, got %d", cfg.MaxIterations)
	}
}

func TestLoad_InvalidDefaultMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ggcode.yaml")
	content := `
vendor: zai
endpoint: cn-coding-openai
model: test
default_mode: turbo
vendors:
  zai:
    api_key: key
    endpoints:
      cn-coding-openai:
        protocol: openai
        base_url: https://example.com
`
	os.WriteFile(path, []byte(content), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for default_mode")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}
	if cfg.SystemPrompt == "" {
		t.Error("DefaultConfig() has empty system prompt")
	}
	if cfg.Vendor == "" || cfg.Endpoint == "" || cfg.Model == "" {
		t.Fatal("DefaultConfig() should set vendor, endpoint, and model")
	}
}

func TestDefaultConfigIncludesBundledVendorCatalog(t *testing.T) {
	cfg := DefaultConfig()

	wantVendors := map[string]string{
		"zai":        "Z.ai",
		"anthropic":  "Anthropic",
		"openai":     "OpenAI",
		"google":     "Google Gemini",
		"openrouter": "OpenRouter",
		"groq":       "Groq",
		"mistral":    "Mistral",
		"deepseek":   "DeepSeek",
		"moonshot":   "Moonshot AI",
		"kimi":       "Kimi Coding Plan",
		"minimax":    "MiniMax Token Plan",
		"ark":        "Volcengine Ark Coding Plan",
		"together":   "Together AI",
		"perplexity": "Perplexity",
	}

	for id, displayName := range wantVendors {
		vendor, ok := cfg.Vendors[id]
		if !ok {
			t.Fatalf("expected default vendor %q to be present", id)
		}
		if vendor.DisplayName != displayName {
			t.Fatalf("expected vendor %q display name %q, got %q", id, displayName, vendor.DisplayName)
		}
		if len(vendor.Endpoints) == 0 {
			t.Fatalf("expected vendor %q to have at least one endpoint", id)
		}
	}

	if got := cfg.Vendors["google"].Endpoints["api"].Protocol; got != "gemini" {
		t.Fatalf("expected google vendor to use gemini protocol, got %q", got)
	}
	if got := cfg.Vendors["anthropic"].Endpoints["api"].Protocol; got != "anthropic" {
		t.Fatalf("expected anthropic vendor to use anthropic protocol, got %q", got)
	}
	if got := cfg.Vendors["openrouter"].Endpoints["api"].Protocol; got != "openai" {
		t.Fatalf("expected openrouter vendor to use openai-compatible protocol, got %q", got)
	}
}

func TestResolveActiveEndpointUsesExplicitContextWindow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o-mini"
	cfg.Vendors["openai"].Endpoints["api"] = EndpointConfig{
		DisplayName:   "OpenAI API",
		Protocol:      "openai",
		BaseURL:       "https://api.openai.com/v1",
		DefaultModel:  "gpt-4o-mini",
		SelectedModel: "gpt-4o-mini",
		ContextWindow: 64000,
		MaxTokens:     4096,
	}

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if resolved.ContextWindow != 64000 {
		t.Fatalf("expected explicit context window 64000, got %d", resolved.ContextWindow)
	}
}

func TestResolveActiveEndpointInfersContextWindowFromModel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "perplexity"
	cfg.Endpoint = "api"
	cfg.Model = "llama-3.1-sonar-small-128k-online"

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if resolved.ContextWindow != 128000 {
		t.Fatalf("expected inferred context window 128000, got %d", resolved.ContextWindow)
	}
}

func TestResolveActiveEndpointInfersGLMCapabilities(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "zai"
	cfg.Endpoint = "cn-coding-openai"
	cfg.Model = "glm-5.1"
	cfg.Vendors["zai"].Endpoints["cn-coding-openai"] = EndpointConfig{
		DisplayName:   "CN Coding Plan",
		Protocol:      "openai",
		BaseURL:       "https://open.bigmodel.cn/api/coding/paas/v4",
		DefaultModel:  "glm-5-turbo",
		SelectedModel: "glm-5-turbo",
		Models:        []string{"glm-5.1"},
	}

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if resolved.ContextWindow != 200000 {
		t.Fatalf("expected GLM context window 200000, got %d", resolved.ContextWindow)
	}
	if resolved.MaxTokens != 128000 {
		t.Fatalf("expected GLM max output 128000, got %d", resolved.MaxTokens)
	}
	if resolved.SupportsVision {
		t.Fatal("expected GLM coding endpoint to default to non-vision")
	}
}

func TestResolveActiveEndpointInfersVisionSupport(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o"

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if !resolved.SupportsVision {
		t.Fatal("expected gpt-4o endpoint to infer vision support")
	}
}

func TestResolveActiveEndpointInfersKimiVisionSupport(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "kimi"
	cfg.Endpoint = "coding-openai"
	cfg.Model = "kimi-k2.5"

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if !resolved.SupportsVision {
		t.Fatal("expected kimi-k2.5 endpoint to infer vision support")
	}
}

func TestResolveActiveEndpointInfersQwenVisionSupport(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "qwen3.6-plus"

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if !resolved.SupportsVision {
		t.Fatal("expected qwen3.6-plus endpoint to infer vision support")
	}
}

func TestResolveActiveEndpointInfersGLMVVisionSupport(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "glm-4v-plus"

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if !resolved.SupportsVision {
		t.Fatal("expected glm-4v-plus endpoint to infer vision support")
	}
}

func TestDefaultConfigUsesGLMCapabilitiesForZaiCatalog(t *testing.T) {
	cfg := DefaultConfig()
	ep := cfg.Vendors["zai"].Endpoints["cn-coding-openai"]
	if ep.ContextWindow != 200000 {
		t.Fatalf("expected default ZAI coding context 200000, got %d", ep.ContextWindow)
	}
	if ep.MaxTokens != 128000 {
		t.Fatalf("expected default ZAI coding max output 128000, got %d", ep.MaxTokens)
	}
	if cfg.Vendors["zai"].Endpoints["cn-coding-anthropic"].DefaultModel != "glm-5-turbo" {
		t.Fatalf("expected anthropic endpoint default model glm-5-turbo")
	}
}

func TestDefaultConfigIncludesKimiCodingPlanCapabilities(t *testing.T) {
	cfg := DefaultConfig()
	ep := cfg.Vendors["kimi"].Endpoints["coding-openai"]
	if ep.DefaultModel != "kimi-for-coding" {
		t.Fatalf("expected kimi default model kimi-for-coding, got %q", ep.DefaultModel)
	}
	if ep.ContextWindow != 262144 {
		t.Fatalf("expected kimi context window 262144, got %d", ep.ContextWindow)
	}
	if ep.MaxTokens != 32768 {
		t.Fatalf("expected kimi max output 32768, got %d", ep.MaxTokens)
	}
}

func TestResolveActiveEndpointLoadsCopilotOAuthState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := auth.DefaultStore()
	if err := store.Save(&auth.Info{
		ProviderID:    auth.ProviderGitHubCopilot,
		Type:          "oauth",
		AccessToken:   "copilot-token",
		EnterpriseURL: "ghe.example.com",
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cfg := DefaultConfig()
	cfg.Vendor = auth.ProviderGitHubCopilot
	cfg.Endpoint = "enterprise"
	cfg.Model = "gpt-4o"

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint() error = %v", err)
	}
	if resolved.Protocol != "copilot" {
		t.Fatalf("expected copilot protocol, got %q", resolved.Protocol)
	}
	if resolved.AuthType != "oauth" {
		t.Fatalf("expected oauth auth type, got %q", resolved.AuthType)
	}
	if resolved.APIKey != "copilot-token" {
		t.Fatalf("expected oauth access token, got %q", resolved.APIKey)
	}
	if resolved.BaseURL != "https://copilot-api.ghe.example.com" {
		t.Fatalf("expected enterprise copilot base URL, got %q", resolved.BaseURL)
	}
}

func TestDefaultConfigIncludesMiniMaxTokenPlanCapabilities(t *testing.T) {
	cfg := DefaultConfig()
	ep := cfg.Vendors["minimax"].Endpoints["token-plan-openai"]
	if ep.DefaultModel != "MiniMax-M2.7" {
		t.Fatalf("expected minimax default model MiniMax-M2.7, got %q", ep.DefaultModel)
	}
	if ep.ContextWindow != 204800 {
		t.Fatalf("expected minimax context window 204800, got %d", ep.ContextWindow)
	}
	if ep.MaxTokens != 2048 {
		t.Fatalf("expected minimax max output 2048, got %d", ep.MaxTokens)
	}
	global := cfg.Vendors["minimax"].Endpoints["global-openai"]
	if global.BaseURL != "https://api.minimax.io/v1" {
		t.Fatalf("expected minimax global openai base url, got %q", global.BaseURL)
	}
	if global.DefaultModel != "MiniMax-M2.7" {
		t.Fatalf("expected minimax global default model MiniMax-M2.7, got %q", global.DefaultModel)
	}
	if global.ContextWindow != 204800 {
		t.Fatalf("expected minimax global context window 204800, got %d", global.ContextWindow)
	}
	if global.MaxTokens != 2048 {
		t.Fatalf("expected minimax global max output 2048, got %d", global.MaxTokens)
	}
}

func TestDefaultConfigIncludesArkCodingPlanCapabilities(t *testing.T) {
	cfg := DefaultConfig()
	openai := cfg.Vendors["ark"].Endpoints["coding-openai"]
	if openai.BaseURL != "https://ark.cn-beijing.volces.com/api/coding/v3" {
		t.Fatalf("expected ark openai base url, got %q", openai.BaseURL)
	}
	if openai.DefaultModel != "ark-code-latest" {
		t.Fatalf("expected ark default model ark-code-latest, got %q", openai.DefaultModel)
	}
	if openai.ContextWindow != 200000 {
		t.Fatalf("expected ark context window 200000, got %d", openai.ContextWindow)
	}
	if openai.MaxTokens != 8192 {
		t.Fatalf("expected ark default max output 8192, got %d", openai.MaxTokens)
	}
	anthropic := cfg.Vendors["ark"].Endpoints["coding-anthropic"]
	if anthropic.BaseURL != "https://ark.cn-beijing.volces.com/api/coding" {
		t.Fatalf("expected ark anthropic base url, got %q", anthropic.BaseURL)
	}
	if anthropic.ContextWindow != 200000 {
		t.Fatalf("expected ark anthropic context window 200000, got %d", anthropic.ContextWindow)
	}
}

func TestUpsertMCPServerReplacesByName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MCPServers = []MCPServerConfig{{
		Name:    "12306-mcp",
		Type:    "stdio",
		Command: "old",
	}}

	replaced := cfg.UpsertMCPServer(MCPServerConfig{
		Name:    "12306-mcp",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "12306-mcp", "stdio"},
	})
	if !replaced {
		t.Fatal("expected existing MCP server to be replaced")
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Command != "npx" {
		t.Fatalf("unexpected MCP server list after replace: %+v", cfg.MCPServers)
	}
}

func TestRemoveMCPServerRemovesByName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MCPServers = []MCPServerConfig{
		{Name: "one", Type: "stdio", Command: "a"},
		{Name: "two", Type: "stdio", Command: "b"},
	}

	if !cfg.RemoveMCPServer("one") {
		t.Fatal("expected RemoveMCPServer to remove existing server")
	}
	if len(cfg.MCPServers) != 1 || cfg.MCPServers[0].Name != "two" {
		t.Fatalf("unexpected MCP servers after remove: %+v", cfg.MCPServers)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
