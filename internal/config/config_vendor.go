package config

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
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
					// #1300: Save errors were discarded. Anthropic rotates the
					// refresh token on each refresh, so a failed Save leaves the
					// OLD (now server-side invalidated) refresh token on disk -
					// the next refresh gets invalid_grant with no self-healing
					// path. Surface the error so the caller/user can re-auth.
					if saveErr := auth.DefaultStore().Save(refreshed); saveErr != nil {
						// #1336: returning the error (not just logging) makes the
						// failure visible to the 27+ ResolveActiveEndpoint callers
						// that key off the returned error to trigger re-auth.
						// The in-memory token would keep THIS session alive while
						// disk holds the server-side invalidated old refresh token:
						// after restart the only outcome is invalid_grant and a
						// forced /login anyway - fail now, while the user is present.
						debug.Log("config", "claude oauth: token refreshed but persisting failed (refresh token lost on restart): %v", saveErr)
						return nil, fmt.Errorf("claude oauth: refreshed token could not be persisted (disk/permission error); re-authenticate before restarting: %w", saveErr)
					}
					apiKey = strings.TrimSpace(refreshed.AccessToken)
				} else {
					// #1300: do NOT silently fall back to the known-expired
					// access token - downstream requests would 401 and mask the
					// real cause (refresh failure). Fail loudly to trigger
					// re-authentication.
					debug.Log("config", "claude oauth: token refresh failed (re-authentication required): %v", refreshErr)
					return nil, fmt.Errorf("claude oauth token refresh failed (run /login to re-authenticate): %w", refreshErr)
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
		ToolChoice:      strings.TrimSpace(ep.ToolChoice),
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
		if ep, ok := vc.Endpoints[endpoint]; ok {
			epName := ep.DisplayName
			if epName == "" {
				epName = endpoint // fallback to raw ID
			}
			endpointDisplay = localizedEndpointDisplay(vendor, endpoint, epName, c.Language)
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
	// Explicit re-add revives the name: clear its deletion tombstone so the
	// merge no longer filters it out.
	c.clearMCPDeleted(server.Name)
	for i, existing := range c.MCPServers {
		if existing.Name != server.Name {
			continue
		}
		c.MCPServers[i] = patchMCPServerConfig(existing, server)
		return true
	}
	c.MCPServers = append(c.MCPServers, server)
	return false
}

// patchMCPServerConfig overlays the provided fields of patch onto base and
// returns the merged config. Semantics per field kind (#249, #606 A3):
//   - strings: empty means "not provided", keeps the existing value
//   - Args/Env/Headers: nil means "not provided" (keep the existing value);
//     an EMPTY non-nil slice/map means "explicitly cleared" and replaces the
//     old value. Before #606 the len()>0 guard conflated the two, so saving
//     a form with every env line deleted silently kept the old env.
//
// When switching server types, type-incompatible fields are cleared:
// - http/sse types: Command and Args are cleared
// - stdio type: URL and Headers are cleared (#584 M2-case2)
func patchMCPServerConfig(base, patch MCPServerConfig) MCPServerConfig {
	merged := base

	// Type switch: clear type-incompatible fields when type changes
	if patch.Type != "" && patch.Type != merged.Type {
		// Switching to http or sse: clear stdio fields
		if patch.Type == "http" || patch.Type == "sse" {
			merged.Command = ""
			merged.Args = nil
		}
		// Switching to stdio: clear http/sse fields
		if patch.Type == "stdio" {
			merged.URL = ""
			merged.Headers = nil
		}
	}

	if patch.Type != "" {
		merged.Type = patch.Type
	}
	if patch.Command != "" {
		merged.Command = patch.Command
	}
	if patch.URL != "" {
		merged.URL = patch.URL
	}
	if patch.Args != nil {
		merged.Args = patch.Args
	}
	if patch.Env != nil {
		merged.Env = patch.Env
	}
	if patch.Headers != nil {
		merged.Headers = patch.Headers
	}
	return merged
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
		c.recordMCPDeleted(name)
		return true
	}
	return false
}

// RecordMCPDeleted is the exported tombstone entry point for surfaces that
// delete servers which exist only in migration sources (never in the yaml):
// cfg.RemoveMCPServer records internally, but returns false for those names
// and never reaches it.
func (c *Config) RecordMCPDeleted(name string) { c.recordMCPDeleted(name) }

// recordMCPDeleted appends name to the deletion tombstones and persists them
// so a Claude source file rewritten behind our back (e.g. Pen.app re-adding
// its registration) cannot resurrect the server via merge. Callers that
// already persist separately (RemoveMCPServer paths save the server list
// right after) still get the tombstone file written here immediately.
func (c *Config) recordMCPDeleted(name string) {
	if c == nil || strings.TrimSpace(name) == "" {
		return
	}
	for _, existing := range c.DeletedMCPServers {
		if existing == name {
			return
		}
	}
	c.DeletedMCPServers = append(c.DeletedMCPServers, name)
	if err := SaveMCPDeleted(c.externalConfigDir(), c.DeletedMCPServers); err != nil {
		debug.Log("config", "failed to save mcp tombstones: %v", err)
	}
}

// clearMCPDeleted removes name from the tombstones (re-adding a server
// revives it) and persists the change.
func (c *Config) clearMCPDeleted(name string) {
	if c == nil {
		return
	}
	kept := c.DeletedMCPServers[:0]
	for _, existing := range c.DeletedMCPServers {
		if existing != name {
			kept = append(kept, existing)
		}
	}
	if len(kept) == len(c.DeletedMCPServers) {
		return // nothing removed
	}
	c.DeletedMCPServers = kept
	if err := SaveMCPDeleted(c.externalConfigDir(), c.DeletedMCPServers); err != nil {
		debug.Log("config", "failed to save mcp tombstones: %v", err)
	}
}

// SaveMCPServers persists the current c.MCPServers slice to mcp_servers.yaml.
// Uses the package-level SaveMCPServers which writes the standalone external
// file directly (not the main config file).
func (c *Config) SaveMCPServers() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	configDir := c.externalConfigDir()
	return SaveMCPServers(configDir, c.MCPServers)
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
