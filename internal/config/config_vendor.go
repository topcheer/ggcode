package config

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/util"
	"gopkg.in/yaml.v3"
)

// ResolveActiveEndpoint resolves the selected vendor + endpoint into runtime settings.
func (c *Config) ResolveActiveEndpoint() (*ResolvedEndpoint, error) {
	return c.ResolveEndpointSelection(c.Vendor, c.Endpoint, c.Model)
}

// ResolveEndpoint resolves the given vendor + endpoint into runtime settings.
func (c *Config) ResolveEndpoint(vendor, endpoint string) (*ResolvedEndpoint, error) {
	return c.ResolveEndpointSelection(vendor, endpoint, "")
}

// ResolveEndpointSelection resolves the given vendor + endpoint + optional explicit model.
func (c *Config) ResolveEndpointSelection(vendor, endpoint, model string) (*ResolvedEndpoint, error) {
	if c == nil {
		return nil, fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return nil, fmt.Errorf("vendor %q is not configured", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return nil, fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = strings.TrimSpace(ep.SelectedModel)
	}
	if model == "" {
		model = strings.TrimSpace(ep.DefaultModel)
	}
	if model == "" {
		return nil, fmt.Errorf("endpoint %q for vendor %q has no active model", endpoint, vendor)
	}
	// Resolve API key: endpoint key first, but if it's an unresolvable ${VAR}
	// reference (env var not set), fall back to vendor key.
	apiKey := resolveEffectiveAPIKeyRef(ep.APIKey, vc.APIKey)
	// Expand any remaining ${VAR} references so the resolved endpoint always
	// contains the actual key value, not a reference string.
	apiKey = ExpandEnv(apiKey)
	authType := strings.TrimSpace(ep.AuthType)
	if authType == "" {
		authType = "api_key"
	}
	baseURL := strings.TrimSpace(ep.BaseURL)
	enterpriseURL := ""
	if authType == "oauth" && vendor == auth.ProviderGitHubCopilot {
		info, err := auth.DefaultStore().Load(auth.ProviderGitHubCopilot)
		if err != nil {
			return nil, err
		}
		if info != nil {
			if apiKey == "" {
				apiKey = strings.TrimSpace(info.AccessToken)
			}
			enterpriseURL = strings.TrimSpace(info.EnterpriseURL)
			if endpoint == "enterprise" && enterpriseURL != "" {
				baseURL = auth.CopilotAPIBaseURL(enterpriseURL)
			} else if endpoint == "github.com" {
				baseURL = auth.CopilotAPIBaseURL("")
			}
		}
	}
	if authType == "oauth" && vendor == auth.ProviderAnthropic {
		info, err := auth.DefaultStore().Load(auth.ProviderAnthropic)
		if err != nil {
			return nil, err
		}
		if info != nil {
			if info.IsExpired() && strings.TrimSpace(info.RefreshToken) != "" {
				refreshed, refreshErr := auth.RefreshClaudeToken(context.Background(), info.RefreshToken)
				if refreshErr == nil && refreshed != nil {
					_ = auth.DefaultStore().Save(refreshed)
					apiKey = strings.TrimSpace(refreshed.AccessToken)
				} else {
					apiKey = strings.TrimSpace(info.AccessToken)
				}
			} else {
				apiKey = strings.TrimSpace(info.AccessToken)
			}
		}
	}
	if baseURL == "" {
		return nil, fmt.Errorf("endpoint %q for vendor %q has no base_url configured", endpoint, vendor)
	}
	// Resolution priority for limits: per-model override -> endpoint-level -> inference.
	maxTokens := 0
	contextWindow := 0
	if ml, ok := ep.ModelLimits[model]; ok {
		maxTokens = ml.MaxTokens
		contextWindow = ml.ContextWindow
	}
	if maxTokens == 0 {
		maxTokens = ep.MaxTokens
	}
	if maxTokens == 0 {
		maxTokens = inferMaxOutputTokens(model, ep.Protocol)
	}
	if contextWindow <= 0 {
		contextWindow = ep.ContextWindow
	}
	if contextWindow <= 0 {
		contextWindow = inferContextWindow(model, ep.Protocol)
	}
	supportsVision := inferVisionSupport(model, ep.Protocol)
	if ep.SupportsVision != nil {
		supportsVision = *ep.SupportsVision
	}
	return &ResolvedEndpoint{
		VendorID:        vendor,
		VendorName:      localizedVendorDisplay(vendor, util.FirstNonEmpty(vc.DisplayName, vendor), c.Language),
		EndpointID:      endpoint,
		EndpointName:    localizedEndpointDisplay(vendor, endpoint, util.FirstNonEmpty(ep.DisplayName, endpoint), c.Language),
		Protocol:        ep.Protocol,
		AuthType:        authType,
		BaseURL:         baseURL,
		APIKey:          apiKey,
		EnterpriseURL:   enterpriseURL,
		Model:           model,
		ContextWindow:   contextWindow,
		MaxTokens:       maxTokens,
		ReasoningEffort: strings.TrimSpace(ep.ReasoningEffort),
		SupportsVision:  supportsVision,
		Models:          append([]string(nil), ep.Models...),
		Tags:            append([]string(nil), ep.Tags...),
	}, nil
}

// ResolveDisplayName resolves vendor and endpoint keys to their display names.
// Falls back to the raw key if the vendor/endpoint is not found or DisplayName is empty.
// This is a lightweight lookup that does not require API keys or model resolution.
func (c *Config) ResolveDisplayName(vendor, endpoint string) (vendorDisplay, endpointDisplay string) {
	vendorDisplay = vendor
	endpointDisplay = endpoint
	if c == nil {
		return
	}
	if vc, ok := c.Vendors[vendor]; ok {
		if vc.DisplayName != "" {
			vendorDisplay = localizedVendorDisplay(vendor, vc.DisplayName, c.Language)
		}
		if ep, ok := vc.Endpoints[endpoint]; ok && ep.DisplayName != "" {
			endpointDisplay = localizedEndpointDisplay(vendor, endpoint, ep.DisplayName, c.Language)
		}
	}
	return
}

// VendorNames returns configured vendors in a stable order.
func (c *Config) VendorNames() []string {
	if c == nil {
		return nil
	}
	names := make([]string, 0, len(c.Vendors))
	for name := range c.Vendors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// EndpointNames returns configured endpoints for the given vendor in a stable order.
func (c *Config) EndpointNames(vendor string) []string {
	if c == nil {
		return nil
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(vc.Endpoints))
	for name := range vc.Endpoints {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ActiveEndpointConfig returns a copy of the active endpoint config.
func (c *Config) ActiveEndpointConfig() *EndpointConfig {
	if c == nil {
		return nil
	}
	vc, ok := c.Vendors[c.Vendor]
	if !ok {
		return nil
	}
	ep, ok := vc.Endpoints[c.Endpoint]
	if !ok {
		return nil
	}
	return &ep
}

// SetActiveSelection updates the current vendor, endpoint, and model.
func (c *Config) SetActiveSelection(vendor, endpoint, model string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
	}
	if model == "" {
		model = util.FirstNonEmpty(ep.SelectedModel, ep.DefaultModel)
	}
	if model == "" {
		return fmt.Errorf("endpoint %q for vendor %q has no model configured", endpoint, vendor)
	}
	ep.SelectedModel = model
	vc.Endpoints[endpoint] = ep
	c.Vendors[vendor] = vc
	c.Vendor = vendor
	c.Endpoint = endpoint
	c.Model = model
	return nil
}

// SetEndpointModels replaces the known models for a configured endpoint while preserving active selections.
func (c *Config) SetEndpointModels(vendor, endpoint string, models []string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
	}
	ep.Models = uniqueNonEmptyStrings(append(models, ep.SelectedModel)...)
	vc.Endpoints[endpoint] = ep
	c.Vendors[vendor] = vc
	if c.Vendor == vendor && c.Endpoint == endpoint {
		c.normalizeActiveModel()
	}
	return nil
}

func (c *Config) UpsertMCPServer(server MCPServerConfig) (replaced bool) {
	if c == nil {
		return false
	}
	for i, existing := range c.MCPServers {
		if existing.Name != server.Name {
			continue
		}
		c.MCPServers[i] = server
		return true
	}
	c.MCPServers = append(c.MCPServers, server)
	return false
}

func (c *Config) RemoveMCPServer(name string) bool {
	if c == nil {
		return false
	}
	for i, server := range c.MCPServers {
		if server.Name != name {
			continue
		}
		c.MCPServers = append(c.MCPServers[:i], c.MCPServers[i+1:]...)
		return true
	}
	return false
}

// SaveMCPServers persists the current c.MCPServers slice to the config file
// using patchConfigFile, which replaces the entire mcp_servers array. This
// is necessary because Save()-based merge would treat an empty slice as a
// zero value and skip it, preserving stale entries in the file.
func (c *Config) SaveMCPServers() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	return c.patchConfigFile(func(raw map[string]interface{}) {
		if len(c.MCPServers) == 0 {
			delete(raw, "mcp_servers")
			return
		}
		serversData, _ := yaml.Marshal(c.MCPServers)
		var serversList []interface{}
		yaml.Unmarshal(serversData, &serversList)
		raw["mcp_servers"] = serversList
	})
}

// SaveMCPServersScoped is like SaveMCPServers but sets the save scope first.
// Use this when the caller has its own scope tracking (e.g. TUI, WebUI)
// to ensure the patch writes to the correct config file.
func (c *Config) SaveMCPServersScoped(scope string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	c.saveScope = scope
	return c.SaveMCPServers()
}

// AddEndpoint creates a new endpoint under the given vendor. If the endpoint
// already exists it is updated. The endpoint name is sanitized for use as a
// YAML map key (lowercase, no spaces).
func (c *Config) AddEndpoint(vendor, endpointName, protocol, baseURL, apiKey string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}
	if endpointName == "" {
		return fmt.Errorf("endpoint name cannot be empty")
	}
	if protocol == "" {
		protocol = "openai"
	}

	ep := EndpointConfig{
		Protocol: protocol,
		BaseURL:  strings.TrimSpace(baseURL),
	}

	// Handle API key: plaintext → env ref + os.Setenv, or pass through ${VAR}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		if _, isRef := envReferenceVarName(apiKey); isRef {
			ep.APIKey = apiKey
		} else {
			envVarName := preferredEndpointAPIKeyEnvVar(vendor, endpointName)
			os.Setenv(envVarName, apiKey)
			ep.APIKey = "${" + envVarName + "}"
		}
	}

	if vc.Endpoints == nil {
		vc.Endpoints = make(map[string]EndpointConfig)
	}
	vc.Endpoints[endpointName] = ep
	c.Vendors[vendor] = vc
	return nil
}

// RemoveEndpoint removes an endpoint from the given vendor.
func (c *Config) RemoveEndpoint(vendor, endpoint string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}
	if _, ok := vc.Endpoints[endpoint]; !ok {
		return fmt.Errorf("endpoint %q does not exist under vendor %q", endpoint, vendor)
	}
	delete(vc.Endpoints, endpoint)
	c.Vendors[vendor] = vc
	return nil
}

// AddVendor creates a new vendor with optional display name and API key.
func (c *Config) AddVendor(name, displayName, apiKey string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if name == "" {
		return fmt.Errorf("vendor name cannot be empty")
	}
	if _, ok := c.Vendors[name]; ok {
		return fmt.Errorf("vendor %q already exists", name)
	}
	vc := VendorConfig{
		DisplayName: displayName,
		Endpoints:   make(map[string]EndpointConfig),
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		if _, isRef := envReferenceVarName(apiKey); isRef {
			vc.APIKey = apiKey
		} else {
			envVarName := preferredEndpointAPIKeyEnvVar(name, "default")
			os.Setenv(envVarName, apiKey)
			vc.APIKey = "${" + envVarName + "}"
		}
	}
	c.Vendors[name] = vc
	return nil
}

// RemoveVendor removes a vendor entirely.
func (c *Config) RemoveVendor(name string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if _, ok := c.Vendors[name]; !ok {
		return fmt.Errorf("vendor %q not found", name)
	}
	delete(c.Vendors, name)
	return nil
}

// SetEndpointModelLimits persists context_window and max_tokens to the
// endpoint config in the global config file. A value of 0 means "unset"
// and will clear the field. The config is saved to the global scope.
func (c *Config) SetEndpointModelLimits(vendor, endpoint string, contextWindow, maxTokens int) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	vc, ok := c.Vendors[vendor]
	if !ok {
		return fmt.Errorf("vendor %q is not configured", vendor)
	}
	ep, ok := vc.Endpoints[endpoint]
	if !ok {
		return fmt.Errorf("endpoint %q is not configured for vendor %q", endpoint, vendor)
	}
	ep.ContextWindow = contextWindow
	ep.MaxTokens = maxTokens
	vc.Endpoints[endpoint] = ep
	c.Vendors[vendor] = vc
	return c.SaveScoped("global")
}
