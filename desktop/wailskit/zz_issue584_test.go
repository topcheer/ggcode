package wailskit

import (
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestC1_BaseURLInstanceWriteback verifies that updating baseURL via UpdateConfig
// has the correct mapping to trigger instance writeback (#584 C1).
func TestC1_BaseURLInstanceWriteback(t *testing.T) {
	// Verify baseURL is in updateConfigInstanceKeys mapping
	if _, ok := updateConfigInstanceKeys["baseURL"]; !ok {
		t.Error("baseURL must be in updateConfigInstanceKeys mapping (#584 C1)")
	}
}

// TestC3_APIKeySetEnvVarResolution verifies that GetFullConfig resolves
// ${VAR} references before checking if API key is set (#584 C3).
func TestC3_APIKeySetEnvVarResolution(t *testing.T) {
	cfg := &config.Config{
		Vendor:   "test-vendor",
		Endpoint: "test-endpoint",
		Vendors: map[string]config.VendorConfig{
			"test-vendor": {
				Endpoints: map[string]config.EndpointConfig{
					"test-endpoint": {
						APIKey: "${MISSING_VAR}",
					},
				},
			},
		},
	}

	globalMu.Lock()
	globalCfg = cfg
	globalMu.Unlock()

	fc, err := GetFullConfig()
	if err != nil {
		t.Fatalf("GetFullConfig failed: %v", err)
	}

	if fc.APIKeySet {
		t.Error("apiKeySet should be false for unresolvable ${MISSING_VAR} (#584 C3)")
	}
}

// TestM1_ParseShellArgsUnbalancedQuote verifies that unbalanced quotes
// are detected and return an error (#584 M1).
func TestM1_ParseShellArgsUnbalancedQuote(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "unbalanced double quote",
			input:   `--config "/Users/John Doe/app.json --verbose`,
			wantErr: true,
		},
		{
			name:    "unbalanced single quote",
			input:   `--config '/Users/John/app.json --verbose`,
			wantErr: true,
		},
		{
			name:    "balanced quotes",
			input:   `--config "/Users/John Doe/app.json" --verbose`,
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseShellArgs(tt.input)
			if tt.wantErr && err == nil {
				t.Errorf("parseShellArgs(%q): expected error, got result %v", tt.input, result)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("parseShellArgs(%q): unexpected error: %v", tt.input, err)
			}
		})
	}
}

// TestM1_ParseShellArgsEscapedQuotes verifies that escaped quotes
// are handled correctly (backslash removed, quote not toggled) (#584 M1).
func TestM1_ParseShellArgsEscapedQuotes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "escaped double quotes",
			input:    `-c "hello \"world\""`,
			expected: []string{"-c", `hello "world"`},
		},
		{
			name:     "escaped single quotes",
			input:    `-c 'hello \'world\''`,
			expected: []string{"-c", `hello 'world'`},
		},
		{
			name:     "backslash before non-quote",
			input:    `path\\to\\file`,
			expected: []string{`path\\to\\file`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseShellArgs(tt.input)
			if err != nil {
				t.Fatalf("parseShellArgs(%q) failed: %v", tt.input, err)
			}
			if len(result) != len(tt.expected) {
				t.Errorf("got %d args, want %d", len(result), len(tt.expected))
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("arg[%d]: got %q, want %q", i, result[i], tt.expected[i])
				}
			}
		})
	}
}

// TestC2_GhostEndpointInstantiation verifies that UpdateConfig with
// baseURL checks endpoint existence before modifying (#584 C2).
func TestC2_GhostEndpointInstantiation(t *testing.T) {
	cfg := &config.Config{
		Vendor:   "test-vendor",
		Endpoint: "nonexistent-endpoint",
		Vendors: map[string]config.VendorConfig{
			"test-vendor": {
				Endpoints: map[string]config.EndpointConfig{
					"real-endpoint": {
						BaseURL:  "https://api.example.com",
						Protocol: "openai",
						Models:   []string{"model1"},
					},
				},
			},
		},
	}

	globalMu.Lock()
	globalCfg = cfg
	globalMu.Unlock()

	err := UpdateConfig(map[string]interface{}{
		"baseURL": "https://new-url.example.com",
	})

	if err == nil {
		t.Error("UpdateConfig should return error for nonexistent endpoint (#584 C2)")
	}
}

// TestM2_TypeSwitchFieldCleanup verifies that UpsertMCPServer clears
// type-incompatible fields when switching server types (#584 M2-case2).
func TestM2_TypeSwitchFieldCleanup(t *testing.T) {
	base := config.MCPServerConfig{
		Name:    "test-server",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "srv"},
		Env:     map[string]string{"NODE_ENV": "production"},
	}

	cfg := &config.Config{
		MCPServers: []config.MCPServerConfig{base},
	}

	patch := config.MCPServerConfig{
		Name: "test-server",
		Type: "http",
		URL:  "http://localhost:3000",
		Env:  map[string]string{"API_KEY": "secret"},
	}

	cfg.UpsertMCPServer(patch)

	var merged *config.MCPServerConfig
	for i := range cfg.MCPServers {
		if cfg.MCPServers[i].Name == "test-server" {
			merged = &cfg.MCPServers[i]
			break
		}
	}

	if merged == nil {
		t.Fatal("server not found after upsert")
	}

	if merged.Command != "" {
		t.Errorf("Command should be empty for http type, got %q", merged.Command)
	}
	if len(merged.Args) > 0 {
		t.Errorf("Args should be empty for http type, got %v", merged.Args)
	}
	if merged.URL != patch.URL {
		t.Errorf("URL should be %q, got %q", patch.URL, merged.URL)
	}
	if len(merged.Env) == 0 {
		t.Error("Env should be preserved (type-agnostic field)")
	}
}

// TestM2_StdioTypeSwitchCleanup verifies switching from http to stdio
// clears http-specific fields (#584 M2-case2).
func TestM2_StdioTypeSwitchCleanup(t *testing.T) {
	base := config.MCPServerConfig{
		Name:    "test-server",
		Type:    "http",
		URL:     "http://localhost:3000",
		Headers: map[string]string{"Authorization": "Bearer token"},
		Env:     map[string]string{"API_KEY": "secret"},
	}

	cfg := &config.Config{
		MCPServers: []config.MCPServerConfig{base},
	}

	patch := config.MCPServerConfig{
		Name:    "test-server",
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"-y", "srv"},
		Env:     map[string]string{"NODE_ENV": "production"},
	}

	cfg.UpsertMCPServer(patch)

	var merged *config.MCPServerConfig
	for i := range cfg.MCPServers {
		if cfg.MCPServers[i].Name == "test-server" {
			merged = &cfg.MCPServers[i]
			break
		}
	}

	if merged == nil {
		t.Fatal("server not found after upsert")
	}

	if merged.URL != "" {
		t.Errorf("URL should be empty for stdio type, got %q", merged.URL)
	}
	if len(merged.Headers) > 0 {
		t.Errorf("Headers should be empty for stdio type, got %v", merged.Headers)
	}
	if merged.Command != patch.Command {
		t.Errorf("Command should be %q, got %q", patch.Command, merged.Command)
	}
	if len(merged.Args) != len(patch.Args) {
		t.Errorf("Args should be %v, got %v", patch.Args, merged.Args)
	}
	if len(merged.Env) == 0 {
		t.Error("Env should be preserved (type-agnostic field)")
	}
}
