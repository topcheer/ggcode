package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// configAccess implements tool.ConfigAccess backed by *config.Config.
// It manages all configuration files: ggcode.yaml, keys.env, harness.yaml, oauth-tokens.
// It does NOT depend on any UI layer type.
type configAccess struct {
	cfg        *config.Config
	workingDir string
	agentInst  *agent.Agent // set after agent creation via SetAgent()
	uiNotify   func()       // optional UI refresh callback
}

// NewConfigAccess creates a ConfigAccess backed by the given config.
func NewConfigAccess(cfg *config.Config, workingDir string) *configAccess {
	return &configAccess{cfg: cfg, workingDir: workingDir}
}

// SetAgent injects the agent instance for provider hot-reload.
// Must be called after agent creation.
func (a *configAccess) SetAgent(ag *agent.Agent) {
	a.agentInst = ag
}

// SetUINotify sets an optional callback for UI refresh after provider changes.
func (a *configAccess) SetUINotify(fn func()) {
	a.uiNotify = fn
}

// --- Get ---

func (a *configAccess) Get(key string) (string, error) {
	if a.cfg == nil {
		return "", fmt.Errorf("config is nil")
	}

	switch {
	// --- Core ---
	case key == "vendor":
		return a.cfg.Vendor, nil
	case key == "endpoint":
		return a.cfg.Endpoint, nil
	case key == "model":
		return a.cfg.Model, nil
	case key == "language":
		return a.cfg.Language, nil
	case key == "default_mode":
		return a.cfg.DefaultMode, nil
	case key == "max_iterations":
		return strconv.Itoa(a.cfg.MaxIterations), nil
	case key == "extra_prompt":
		return a.cfg.ExtraPrompt, nil
	case key == "probe_context":
		return strconv.FormatBool(a.cfg.ProbeContext), nil

	// --- API Keys ---
	case key == "api_key":
		return a.getAPIKey(a.cfg.Vendor, a.cfg.Endpoint)
	case strings.HasPrefix(key, "api_key."):
		return a.getAPIKeyByPath(strings.TrimPrefix(key, "api_key."))
	case key == "api_keys":
		return a.listAPIKeys()

	// --- Vendors ---
	case key == "vendors":
		return a.listVendors()
	case strings.HasPrefix(key, "vendors.") && strings.HasSuffix(key, ".discover_models"):
		return a.discoverModels(key)
	case strings.HasPrefix(key, "vendors.") && strings.HasSuffix(key, ".models"):
		return a.getEndpointModels(key)
	case strings.HasPrefix(key, "vendors."):
		return a.getVendorPath(strings.TrimPrefix(key, "vendors."))

	// --- MCP Servers ---
	case key == "mcp_servers":
		return a.listMCPServers()
	case strings.HasPrefix(key, "mcp_servers."):
		return a.getMCPServer(strings.TrimPrefix(key, "mcp_servers."))

	// --- IM ---
	case key == "im.output_mode":
		return a.cfg.IM.OutputMode, nil
	case key == "im.adapters":
		return a.listIMAdapters()
	case strings.HasPrefix(key, "im.adapters."):
		return a.getIMAdapter(strings.TrimPrefix(key, "im.adapters."))

	// --- A2A ---
	case key == "a2a.disabled":
		return strconv.FormatBool(a.cfg.A2A.Disabled), nil
	case key == "a2a.host":
		return a.cfg.A2A.Host, nil
	case key == "a2a.port":
		return strconv.Itoa(a.cfg.A2A.Port), nil
	case key == "a2a.lan_discovery":
		return strconv.FormatBool(a.cfg.A2A.IsLANDiscovery()), nil
	case strings.HasPrefix(key, "a2a.auth"):
		return a.getA2AAuth(key)

	// --- Knight ---
	case key == "knight.enabled":
		return strconv.FormatBool(a.cfg.KnightConfig.Enabled), nil
	case key == "knight.budget":
		return strconv.Itoa(a.cfg.KnightConfig.DailyTokenBudget), nil
	case key == "knight.idle_seconds":
		return strconv.Itoa(a.cfg.KnightConfig.IdleDelaySec), nil

	// --- Harness (runtime) ---
	case key == "harness.auto_run":
		return a.cfg.Harness.AutoRun, nil
	case key == "harness.auto_init":
		return strconv.FormatBool(a.cfg.Harness.AutoInit), nil

	// --- Runtime ---
	case key == "allowed_dirs":
		b, _ := json.Marshal(a.cfg.AllowedDirs)
		return string(b), nil
	case key == "tool_permissions":
		return a.getToolPermissions()
	case key == "scope":
		return a.cfg.GetSaveScope(), nil

	default:
		return "", fmt.Errorf("unknown config key: %q (use list=true to see all keys)", key)
	}
}

// --- Set ---

func (a *configAccess) Set(key, value string) error {
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}

	// Provider-affecting keys: probe before commit
	switch {
	case key == "vendor", key == "endpoint", key == "model":
		return a.setWithProbe(key, value)
	case key == "api_key":
		return a.setAPIKeyWithProbe(value)
	case strings.HasPrefix(key, "api_key."):
		return a.setAPIKeyByPathWithProbe(strings.TrimPrefix(key, "api_key."), value)
	}

	// Non-provider keys: direct write
	switch {
	// --- Core (non-provider) ---
	case key == "language":
		return a.cfg.SaveLanguagePreference(value)
	case key == "default_mode":
		return a.cfg.SaveDefaultModePreference(value)
	case key == "max_iterations":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid max_iterations: %w", err)
		}
		a.cfg.MaxIterations = n
		return a.saveAndPatch("max_iterations", value)
	case key == "extra_prompt":
		a.cfg.ExtraPrompt = value
		return a.saveAndPatch("extra_prompt", value)
	case key == "probe_context":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid probe_context: %w", err)
		}
		a.cfg.ProbeContext = b
		return a.saveAndPatch("probe_context", value)

	// --- Vendors ---
	case key == "vendors":
		return fmt.Errorf("use 'vendors.<name>' to manage vendors")
	case strings.HasPrefix(key, "vendors."):
		return a.setVendorPath(strings.TrimPrefix(key, "vendors."), value)

	// --- MCP Servers ---
	case strings.HasPrefix(key, "mcp_servers."):
		return a.setMCPServer(strings.TrimPrefix(key, "mcp_servers."), value)

	// --- IM ---
	case key == "im.output_mode":
		a.cfg.IM.OutputMode = value
		return a.saveAndPatch("im.output_mode", value)
	case strings.HasPrefix(key, "im.adapters."):
		return a.setIMAdapterPath(strings.TrimPrefix(key, "im.adapters."), value)

	// --- A2A ---
	case key == "a2a.disabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid a2a.disabled: %w", err)
		}
		a.cfg.A2A.Disabled = b
		return a.saveAndPatch("a2a.disabled", value)
	case key == "a2a.host":
		a.cfg.A2A.Host = value
		return a.saveAndPatch("a2a.host", value)
	case key == "a2a.port":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid a2a.port: %w", err)
		}
		a.cfg.A2A.Port = n
		return a.saveAndPatch("a2a.port", value)
	case key == "a2a.lan_discovery":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid a2a.lan_discovery: %w", err)
		}
		a.cfg.A2A.LANDiscovery = &b
		return a.saveAndPatch("a2a.lan_discovery", value)
	case strings.HasPrefix(key, "a2a.auth"):
		return a.setA2AAuth(key, value)

	// --- Knight ---
	case key == "knight.enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid knight.enabled: %w", err)
		}
		return a.cfg.SaveKnightEnabled(b)
	case key == "knight.budget":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid knight.budget: %w", err)
		}
		a.cfg.KnightConfig.DailyTokenBudget = n
		return a.saveAndPatch("knight.daily_token_budget", value)
	case key == "knight.idle_seconds":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid knight.idle_seconds: %w", err)
		}
		a.cfg.KnightConfig.IdleDelaySec = n
		return a.saveAndPatch("knight.idle_delay_sec", value)

	// --- Harness ---
	case key == "harness.auto_run":
		a.cfg.Harness.AutoRun = value
		return a.saveAndPatch("harness.auto_run", value)
	case key == "harness.auto_init":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid harness.auto_init: %w", err)
		}
		a.cfg.Harness.AutoInit = b
		return a.saveAndPatch("harness.auto_init", value)

	// --- Runtime ---
	case key == "allowed_dirs":
		var dirs []string
		if err := json.Unmarshal([]byte(value), &dirs); err != nil {
			return fmt.Errorf("invalid allowed_dirs (expected JSON array): %w", err)
		}
		a.cfg.AllowedDirs = dirs
		return a.saveAndPatch("allowed_dirs", value)
	case key == "tool_permissions":
		return a.setToolPermissions(value)
	case key == "scope":
		return a.cfg.SetSaveScope(value)

	default:
		return fmt.Errorf("unknown config key: %q", key)
	}
}

// --- List ---

func (a *configAccess) List(section string) (string, error) {
	if a.cfg == nil {
		return "Config is nil\n", nil
	}

	var sb strings.Builder

	switch strings.ToLower(section) {
	case "", "all":
		sb.WriteString(a.listSectionCore())
		sb.WriteString(a.listSectionAPIKey())
		sb.WriteString(a.listSectionVendors())
		sb.WriteString(a.listSectionMCP())
		sb.WriteString(a.listSectionIM())
		sb.WriteString(a.listSectionA2A())
		sb.WriteString(a.listSectionKnight())
		sb.WriteString(a.listSectionHarness())
		sb.WriteString(a.listSectionRuntime())
	case "core":
		sb.WriteString(a.listSectionCore())
	case "api_key":
		sb.WriteString(a.listSectionAPIKey())
	case "vendors", "vendor":
		sb.WriteString(a.listSectionVendors())
	case "mcp", "mcp_servers":
		sb.WriteString(a.listSectionMCP())
	case "im":
		sb.WriteString(a.listSectionIM())
	case "a2a":
		sb.WriteString(a.listSectionA2A())
	case "knight":
		sb.WriteString(a.listSectionKnight())
	case "harness":
		sb.WriteString(a.listSectionHarness())
	case "runtime":
		sb.WriteString(a.listSectionRuntime())
	default:
		return "", fmt.Errorf("unknown section %q (valid: core, api_key, vendors, mcp, im, a2a, knight, harness, runtime)", section)
	}

	return sb.String(), nil
}

// --- Delete ---

func (a *configAccess) Delete(key string) error {
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	switch {
	case strings.HasPrefix(key, "mcp_servers."):
		name := strings.TrimPrefix(key, "mcp_servers.")
		if !a.cfg.RemoveMCPServer(name) {
			return fmt.Errorf("MCP server %q not found", name)
		}
		return a.cfg.SaveScoped(a.cfg.GetSaveScope())
	case strings.HasPrefix(key, "im.adapters."):
		name := strings.TrimPrefix(key, "im.adapters.")
		if err := a.cfg.RemoveIMAdapter(name); err != nil {
			return err
		}
		return nil // RemoveIMAdapter already persists
	default:
		return fmt.Errorf("delete not supported for %q (only mcp_servers.<name> and im.adapters.<name>)", key)
	}
}

// ============================================================================
// Probe
// ============================================================================

// probeProvider sends a minimal Chat request to verify the provider works.
// Returns nil on success (including 429 rate-limit), descriptive error on failure.
func probeProvider(resolved *config.ResolvedEndpoint) error {
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return fmt.Errorf("cannot create provider: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = prov.Chat(ctx, []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock("Reply with exactly: OK")}},
	}, nil)
	if err != nil {
		// Allow 429 (rate limit) — the key is valid, just temporarily limited
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate_limit") {
			debug.Log("config", "probe got 429 rate limit, allowing switch")
			return nil
		}
		return fmt.Errorf("probe failed: %w", err)
	}
	return nil
}

// setWithProbe handles vendor/endpoint/model changes with probe-then-commit.
func (a *configAccess) setWithProbe(key, value string) error {
	newVendor, newEndpoint, newModel := a.cfg.Vendor, a.cfg.Endpoint, a.cfg.Model
	switch key {
	case "vendor":
		newVendor = value
	case "endpoint":
		newEndpoint = value
	case "model":
		newModel = value
	}

	// Resolve target (without writing to cfg)
	testResolved, err := a.cfg.ResolveEndpointSelection(newVendor, newEndpoint, newModel)
	if err != nil {
		return fmt.Errorf("cannot resolve target %s/%s/%s: %w", newVendor, newEndpoint, newModel, err)
	}

	// Probe
	if err := probeProvider(testResolved); err != nil {
		return fmt.Errorf("refusing to switch to %s/%s/%s: probe failed: %w.\nCurrent provider is unchanged (%s/%s/%s).",
			newVendor, newEndpoint, newModel, err,
			a.cfg.Vendor, a.cfg.Endpoint, a.cfg.Model)
	}

	// Probe passed — safe to commit
	if err := a.cfg.SetActiveSelection(newVendor, newEndpoint, newModel); err != nil {
		return fmt.Errorf("SetActiveSelection failed: %w", err)
	}
	if err := a.cfg.Save(); err != nil {
		return fmt.Errorf("save failed: %w", err)
	}

	debug.Log("config", "switched provider to %s/%s/%s (probe OK)", newVendor, newEndpoint, newModel)
	a.reloadProvider()
	return nil
}

// ============================================================================
// API Key helpers
// ============================================================================

func (a *configAccess) getAPIKey(vendor, endpoint string) (string, error) {
	resolved, err := a.cfg.ResolveEndpoint(vendor, endpoint)
	if err != nil {
		return "(unresolvable)", nil
	}
	return maskSecret(resolved.APIKey), nil
}

func (a *configAccess) getAPIKeyByPath(path string) (string, error) {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 1 {
		// api_key.<vendor> — vendor-level key
		vc, ok := a.cfg.Vendors[parts[0]]
		if !ok {
			return "", fmt.Errorf("vendor %q not found", parts[0])
		}
		return maskSecret(config.ExpandEnv(vc.APIKey)), nil
	}
	// api_key.<vendor>.<endpoint>
	vc, ok := a.cfg.Vendors[parts[0]]
	if !ok {
		return "", fmt.Errorf("vendor %q not found", parts[0])
	}
	ep, ok := vc.Endpoints[parts[1]]
	if !ok {
		return "", fmt.Errorf("endpoint %q not found under vendor %q", parts[1], parts[0])
	}
	return maskSecret(config.ExpandEnv(ep.APIKey)), nil
}

func (a *configAccess) listAPIKeys() (string, error) {
	var entries []string
	for vName, vc := range a.cfg.Vendors {
		if vc.APIKey != "" {
			entries = append(entries, fmt.Sprintf("  %s (vendor-level): %s", vName, maskSecret(config.ExpandEnv(vc.APIKey))))
		}
		for epName, ep := range vc.Endpoints {
			if ep.APIKey != "" {
				entries = append(entries, fmt.Sprintf("  %s.%s: %s", vName, epName, maskSecret(config.ExpandEnv(ep.APIKey))))
			}
		}
	}
	if len(entries) == 0 {
		return "(no API keys configured)", nil
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n"), nil
}

func (a *configAccess) setAPIKeyWithProbe(value string) error {
	vendor, endpoint := a.cfg.Vendor, a.cfg.Endpoint

	// Resolve with current key first to get the endpoint config
	testResolved, err := a.cfg.ResolveEndpoint(vendor, endpoint)
	if err != nil {
		return fmt.Errorf("cannot resolve endpoint: %w", err)
	}
	// Override with new key for probe
	testResolved.APIKey = value

	if err := probeProvider(testResolved); err != nil {
		return fmt.Errorf("refusing to set api_key: probe failed: %w.\nCurrent key is unchanged.", err)
	}

	err = a.cfg.SetEndpointAPIKey(vendor, endpoint, value, false)
	if err == nil {
		a.reloadProvider()
	}
	return err
}

func (a *configAccess) setAPIKeyByPathWithProbe(path, value string) error {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 1 {
		// vendor-level key
		vendor := parts[0]
		testResolved, err := a.cfg.ResolveEndpointSelection(vendor, a.cfg.Endpoint, a.cfg.Model)
		if err != nil {
			return fmt.Errorf("cannot resolve: %w", err)
		}
		testResolved.APIKey = value
		if err := probeProvider(testResolved); err != nil {
			return fmt.Errorf("refusing to set api_key for vendor %s: probe failed: %w", vendor, err)
		}
		err = a.cfg.SetVendorAPIKey(vendor, value)
		if err == nil {
			a.reloadProvider()
		}
		return err
	}
	// endpoint-level key
	vendor, endpoint := parts[0], parts[1]
	testResolved, err := a.cfg.ResolveEndpoint(vendor, endpoint)
	if err != nil {
		return fmt.Errorf("cannot resolve: %w", err)
	}
	testResolved.APIKey = value
	if err := probeProvider(testResolved); err != nil {
		return fmt.Errorf("refusing to set api_key for %s/%s: probe failed: %w", vendor, endpoint, err)
	}
	err = a.cfg.SetEndpointAPIKey(vendor, endpoint, value, false)
	if err == nil {
		a.reloadProvider()
	}
	return err
}

// ============================================================================
// Vendor helpers
// ============================================================================

func (a *configAccess) listVendors() (string, error) {
	names := make([]string, 0, len(a.cfg.Vendors))
	for name := range a.cfg.Vendors {
		names = append(names, name)
	}
	sort.Strings(names)
	b, _ := json.Marshal(names)
	return string(b), nil
}

func (a *configAccess) getVendorPath(path string) (string, error) {
	parts := strings.SplitN(path, ".", 2)
	vc, ok := a.cfg.Vendors[parts[0]]
	if !ok {
		return "", fmt.Errorf("vendor %q not found", parts[0])
	}
	if len(parts) == 1 {
		// Summary of the vendor
		summary := map[string]interface{}{
			"display_name": vc.DisplayName,
		}
		epNames := make([]string, 0, len(vc.Endpoints))
		for n := range vc.Endpoints {
			epNames = append(epNames, n)
		}
		summary["endpoints"] = epNames
		b, _ := json.Marshal(summary)
		return string(b), nil
	}
	// vendors.<name>.endpoints or vendors.<name>.api_key
	sub := parts[1]
	switch {
	case sub == "endpoints":
		names := make([]string, 0, len(vc.Endpoints))
		for n := range vc.Endpoints {
			names = append(names, n)
		}
		sort.Strings(names)
		b, _ := json.Marshal(names)
		return string(b), nil
	case sub == "api_key":
		return maskSecret(config.ExpandEnv(vc.APIKey)), nil
	default:
		// vendors.<name>.endpoints.<ep>
		if strings.HasPrefix(sub, "endpoints.") {
			epName := strings.TrimPrefix(sub, "endpoints.")
			ep, ok := vc.Endpoints[epName]
			if !ok {
				return "", fmt.Errorf("endpoint %q not found under vendor %q", epName, parts[0])
			}
			summary := map[string]interface{}{
				"protocol": ep.Protocol,
				"base_url": ep.BaseURL,
			}
			if ep.DefaultModel != "" {
				summary["default_model"] = ep.DefaultModel
			}
			if ep.ContextWindow > 0 {
				summary["context_window"] = ep.ContextWindow
			}
			if len(ep.Models) > 0 {
				summary["models"] = ep.Models
			}
			b, _ := json.Marshal(summary)
			return string(b), nil
		}
		return "", fmt.Errorf("unknown vendor path: vendors.%s", path)
	}
}

func (a *configAccess) setVendorPath(path, value string) error {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 1 {
		return fmt.Errorf("use 'vendors.<name>.endpoints.<ep>' to create/update endpoints")
	}
	// vendors.<name>.endpoints.<ep> — expects JSON with protocol, base_url, api_key
	if strings.HasPrefix(parts[1], "endpoints.") {
		vendor := parts[0]
		epName := strings.TrimPrefix(parts[1], "endpoints.")
		var epData struct {
			Protocol string `json:"protocol"`
			BaseURL  string `json:"base_url"`
			APIKey   string `json:"api_key"`
		}
		if err := json.Unmarshal([]byte(value), &epData); err != nil {
			return fmt.Errorf("invalid endpoint JSON: %w", err)
		}
		if err := a.cfg.AddEndpoint(vendor, epName, epData.Protocol, epData.BaseURL, epData.APIKey); err != nil {
			return err
		}
		return a.cfg.SaveScoped(a.cfg.GetSaveScope())
	}
	return fmt.Errorf("unknown vendor path for write: vendors.%s", path)
}

// ============================================================================
// MCP Server helpers
// ============================================================================

func (a *configAccess) listMCPServers() (string, error) {
	if len(a.cfg.MCPServers) == 0 {
		return "(no MCP servers configured)\n", nil
	}
	names := make([]string, 0, len(a.cfg.MCPServers))
	for _, srv := range a.cfg.MCPServers {
		names = append(names, srv.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ") + "\n", nil
}

func (a *configAccess) getMCPServer(name string) (string, error) {
	for _, srv := range a.cfg.MCPServers {
		if srv.Name == name {
			// Mask secrets in env and headers
			srv = redactMCPServer(srv)
			b, _ := json.MarshalIndent(srv, "", "  ")
			return string(b), nil
		}
	}
	return "", fmt.Errorf("MCP server %q not found", name)
}

func (a *configAccess) setMCPServer(name, value string) error {
	var srv config.MCPServerConfig
	if err := json.Unmarshal([]byte(value), &srv); err != nil {
		return fmt.Errorf("invalid MCP server JSON: %w", err)
	}
	srv.Name = name

	// Migrate plaintext secrets in env to keys.env + env refs
	for key, val := range srv.Env {
		if config.IsPlaintextSecret(val) {
			envVar := config.MCPServerEnvVar(name, key)
			os.Setenv(envVar, val)
			if err := config.WriteKeysEnv(map[string]string{envVar: val}); err != nil {
				debug.Log("config", "failed to persist %s to keys.env: %v", envVar, err)
			}
			srv.Env[key] = "${" + envVar + "}"
		}
	}

	// Migrate plaintext secrets in headers
	for key, val := range srv.Headers {
		if config.IsPlaintextSecret(val) {
			envVar := config.MCPServerHeaderEnvVar(name, key)
			os.Setenv(envVar, val)
			if err := config.WriteKeysEnv(map[string]string{envVar: val}); err != nil {
				debug.Log("config", "failed to persist %s to keys.env: %v", envVar, err)
			}
			srv.Headers[key] = "${" + envVar + "}"
		}
	}

	a.cfg.UpsertMCPServer(srv)
	return a.cfg.SaveScoped(a.cfg.GetSaveScope())
}

// ============================================================================
// IM helpers
// ============================================================================

func (a *configAccess) listIMAdapters() (string, error) {
	if len(a.cfg.IM.Adapters) == 0 {
		return "(no IM adapters configured)\n", nil
	}
	var entries []string
	for name, ad := range a.cfg.IM.Adapters {
		status := "enabled"
		if !ad.Enabled {
			status = "disabled"
		}
		entries = append(entries, fmt.Sprintf("  %s (%s, %s)", name, status, ad.Platform))
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n") + "\n", nil
}

func (a *configAccess) getIMAdapter(name string) (string, error) {
	for adapterName, ad := range a.cfg.IM.Adapters {
		if adapterName == name {
			// Mask secrets in extra
			redacted := redactIMAdapter(ad)
			b, _ := json.MarshalIndent(redacted, "", "  ")
			return string(b), nil
		}
	}
	return "", fmt.Errorf("IM adapter %q not found", name)
}

func (a *configAccess) setIMAdapterPath(path, value string) error {
	// im.adapters.<name> — full adapter config as JSON
	// im.adapters.<name>.<field> — single field
	parts := strings.SplitN(path, ".", 2)
	adapterName := parts[0]

	if len(parts) == 1 || parts[1] == "" {
		// Full adapter config
		var ad config.IMAdapterConfig
		if err := json.Unmarshal([]byte(value), &ad); err != nil {
			return fmt.Errorf("invalid IM adapter JSON: %w", err)
		}

		// Migrate plaintext secrets in extra to keys.env
		for key, val := range ad.Extra {
			if strVal, ok := val.(string); ok && config.IsPlaintextSecret(strVal) && config.LooksLikeSecretField(key) {
				envVar := config.IMAdapterSecretEnvVar(adapterName, key)
				os.Setenv(envVar, strVal)
				if err := config.WriteKeysEnv(map[string]string{envVar: strVal}); err != nil {
					debug.Log("config", "failed to persist %s to keys.env: %v", envVar, err)
				}
				ad.Extra[key] = "${" + envVar + "}"
			}
		}

		return a.cfg.AddIMAdapter(adapterName, ad)
	}

	// Single field: im.adapters.<name>.enabled, im.adapters.<name>.extra.<field>
	field := parts[1]
	switch {
	case field == "enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean: %w", err)
		}
		return a.cfg.SetIMAdapterEnabled(adapterName, b)
	default:
		// Treat as extra field — check if it's a secret
		if config.LooksLikeSecretField(field) && config.IsPlaintextSecret(value) {
			envVar := config.IMAdapterSecretEnvVar(adapterName, field)
			os.Setenv(envVar, value)
			if err := config.WriteKeysEnv(map[string]string{envVar: value}); err != nil {
				debug.Log("config", "failed to persist %s to keys.env: %v", envVar, err)
			}
			return a.cfg.SetIMAdapterExtra(adapterName, field, "${"+envVar+"}")
		}
		return a.cfg.SetIMAdapterExtra(adapterName, field, value)
	}
}

// ============================================================================
// A2A helpers
// ============================================================================

func (a *configAccess) getA2AAuth(key string) (string, error) {
	auth := a.cfg.A2A.Auth
	switch key {
	case "a2a.auth.api_key":
		return maskSecret(config.ExpandEnv(auth.APIKey)), nil
	case "a2a.auth.api_keys":
		masked := make([]string, len(auth.APIKeys))
		for i, k := range auth.APIKeys {
			masked[i] = maskSecret(config.ExpandEnv(k))
		}
		b, _ := json.Marshal(masked)
		return string(b), nil
	case "a2a.auth.oauth2":
		if auth.OAuth2 == nil {
			return "(not configured)", nil
		}
		summary := map[string]string{
			"provider":   auth.OAuth2.Provider,
			"client_id":  auth.OAuth2.ClientID,
			"issuer_url": auth.OAuth2.IssuerURL,
			"flow":       auth.OAuth2.Flow,
		}
		b, _ := json.Marshal(summary)
		return string(b), nil
	case "a2a.auth.oidc":
		if auth.OIDC == nil {
			return "(not configured)", nil
		}
		summary := map[string]string{
			"provider":   auth.OIDC.Provider,
			"client_id":  auth.OIDC.ClientID,
			"issuer_url": auth.OIDC.IssuerURL,
		}
		b, _ := json.Marshal(summary)
		return string(b), nil
	case "a2a.auth.mtls":
		if auth.MTLS == nil {
			return "(not configured)", nil
		}
		b, _ := json.Marshal(map[string]string{
			"cert_file": auth.MTLS.CertFile,
			"key_file":  auth.MTLS.KeyFile,
			"ca_file":   auth.MTLS.CAFile,
		})
		return string(b), nil
	case "a2a.auth.allow_unauthenticated":
		return strconv.FormatBool(auth.AllowUnauthenticated), nil
	default:
		return "", fmt.Errorf("unknown a2a auth key: %q", key)
	}
}

func (a *configAccess) setA2AAuth(key, value string) error {
	switch key {
	case "a2a.auth.api_key":
		return a.setA2ASecret("api_key", value)
	case "a2a.auth.allow_unauthenticated":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean: %w", err)
		}
		a.cfg.A2A.Auth.AllowUnauthenticated = b
		return a.saveAndPatch("a2a.auth.allow_unauthenticated", value)
	case "a2a.auth.oauth2":
		var oauth2 config.A2AOAuth2Config
		if err := json.Unmarshal([]byte(value), &oauth2); err != nil {
			return fmt.Errorf("invalid OAuth2 JSON: %w", err)
		}
		// Migrate client_secret
		if config.IsPlaintextSecret(oauth2.ClientSecret) {
			envVar := config.A2ASecretEnvVar("oauth2_client_secret")
			os.Setenv(envVar, oauth2.ClientSecret)
			if err := config.WriteKeysEnv(map[string]string{envVar: oauth2.ClientSecret}); err != nil {
				debug.Log("config", "failed to persist %s to keys.env: %v", envVar, err)
			}
			oauth2.ClientSecret = "${" + envVar + "}"
		}
		a.cfg.A2A.Auth.OAuth2 = &oauth2
		return a.cfg.SaveScoped(a.cfg.GetSaveScope())
	default:
		return fmt.Errorf("setting %q is not supported yet", key)
	}
}

func (a *configAccess) setA2ASecret(field, value string) error {
	envVar := config.A2ASecretEnvVar(field)
	os.Setenv(envVar, value)
	if err := config.WriteKeysEnv(map[string]string{envVar: value}); err != nil {
		debug.Log("config", "failed to persist %s to keys.env: %v", envVar, err)
	}
	ref := "${" + envVar + "}"
	switch field {
	case "api_key":
		a.cfg.A2A.Auth.APIKey = ref
	}
	return a.cfg.SaveScoped(a.cfg.GetSaveScope())
}

// ============================================================================
// List sections
// ============================================================================

func (a *configAccess) listSectionCore() string {
	return fmt.Sprintf("== Core ==\n  vendor: %s\n  endpoint: %s\n  model: %s\n  language: %s\n  default_mode: %s\n  max_iterations: %d\n  extra_prompt: %s\n  probe_context: %v\n",
		a.cfg.Vendor, a.cfg.Endpoint, a.cfg.Model, a.cfg.Language,
		a.cfg.DefaultMode, a.cfg.MaxIterations,
		truncate(a.cfg.ExtraPrompt, 80), a.cfg.ProbeContext)
}

func (a *configAccess) listSectionAPIKey() string {
	return fmt.Sprintf("== API Keys ==\n  current: %s\n", maskSecret("(see api_key for details)"))
}

func (a *configAccess) listSectionVendors() string {
	var sb strings.Builder
	sb.WriteString("== Vendors ==\n")
	if len(a.cfg.Vendors) == 0 {
		sb.WriteString("  (none)\n")
		return sb.String()
	}
	names := make([]string, 0, len(a.cfg.Vendors))
	for n := range a.cfg.Vendors {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		vc := a.cfg.Vendors[n]
		epCount := len(vc.Endpoints)
		sb.WriteString(fmt.Sprintf("  %s (%d endpoints)\n", n, epCount))
	}
	return sb.String()
}

func (a *configAccess) listSectionMCP() string {
	var sb strings.Builder
	sb.WriteString("== MCP Servers ==\n")
	if len(a.cfg.MCPServers) == 0 {
		sb.WriteString("  (none)\n")
		return sb.String()
	}
	for _, srv := range a.cfg.MCPServers {
		cmd := strings.Join(srv.Args, " ")
		if srv.URL != "" {
			cmd = srv.URL
		}
		sb.WriteString(fmt.Sprintf("  %s: %s\n", srv.Name, truncate(cmd, 60)))
	}
	return sb.String()
}

func (a *configAccess) listSectionIM() string {
	return fmt.Sprintf("== IM ==\n  output_mode: %s\n  adapters: %d\n",
		a.cfg.IM.OutputMode, len(a.cfg.IM.Adapters))
}

func (a *configAccess) listSectionA2A() string {
	auth := a.cfg.A2A.Auth
	methods := []string{}
	if auth.APIKey != "" {
		methods = append(methods, "api_key")
	}
	if auth.OAuth2 != nil {
		methods = append(methods, "oauth2")
	}
	if auth.OIDC != nil {
		methods = append(methods, "oidc")
	}
	if auth.MTLS != nil {
		methods = append(methods, "mtls")
	}
	if len(methods) == 0 {
		methods = append(methods, "(none)")
	}
	return fmt.Sprintf("== A2A ==\n  disabled: %v\n  host: %s\n  port: %d\n  auth: %s\n  lan_discovery: %v\n",
		a.cfg.A2A.Disabled, a.cfg.A2A.Host, a.cfg.A2A.Port,
		strings.Join(methods, "+"), a.cfg.A2A.IsLANDiscovery())
}

func (a *configAccess) listSectionKnight() string {
	return fmt.Sprintf("== Knight ==\n  enabled: %v\n  budget: %d\n  idle_seconds: %d\n",
		a.cfg.KnightConfig.Enabled, a.cfg.KnightConfig.DailyTokenBudget, a.cfg.KnightConfig.IdleDelaySec)
}

func (a *configAccess) listSectionHarness() string {
	return fmt.Sprintf("== Harness ==\n  auto_run: %s\n  auto_init: %v\n",
		a.cfg.Harness.AutoRun, a.cfg.Harness.AutoInit)
}

func (a *configAccess) listSectionRuntime() string {
	var sb strings.Builder
	sb.WriteString("== Runtime ==\n  scope: ")
	if a.cfg.GetSaveScope() != "" {
		sb.WriteString(a.cfg.GetSaveScope())
	} else {
		sb.WriteString("global")
	}
	sb.WriteString("\n  allowed_dirs: ")
	if len(a.cfg.AllowedDirs) == 0 {
		sb.WriteString("(default)")
	} else {
		b, _ := json.Marshal(a.cfg.AllowedDirs)
		sb.WriteString(string(b))
	}
	sb.WriteString("\n")
	return sb.String()
}

func (a *configAccess) getToolPermissions() (string, error) {
	if len(a.cfg.ToolPerms) == 0 {
		return "(none)", nil
	}
	b, _ := json.Marshal(a.cfg.ToolPerms)
	return string(b), nil
}

func (a *configAccess) setToolPermissions(value string) error {
	var perms map[string]config.ToolPermission
	if err := json.Unmarshal([]byte(value), &perms); err != nil {
		return fmt.Errorf("invalid tool_permissions JSON: %w", err)
	}
	a.cfg.ToolPerms = perms
	return a.saveAndPatch("tool_permissions", value)
}

// ============================================================================
// Persistence
// ============================================================================

// saveAndPatch persists a config change using patchConfigFile.
func (a *configAccess) saveAndPatch(key, value string) error {
	scope := a.cfg.GetSaveScope()
	if scope == "" {
		scope = "global"
	}
	return a.cfg.SaveScoped(scope)
}

// ============================================================================
// Masking & Redaction
// ============================================================================

func maskSecret(value string) string {
	if value == "" {
		return "(not set)"
	}
	// If it's an env reference like ${VAR}
	if envVar, ok := config.IsEnvReference(value); ok {
		expanded := os.Getenv(envVar)
		if expanded == "" {
			return "${" + envVar + "} (not set)"
		}
		return "${" + envVar + "} (set, " + maskPlaintext(expanded) + ")"
	}
	// Plaintext — shouldn't exist in YAML but handle defensively
	return maskPlaintext(value)
}

func maskPlaintext(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + strings.Repeat("*", len(s)-8) + s[len(s)-4:]
}

func redactMCPServer(srv config.MCPServerConfig) config.MCPServerConfig {
	// Mask env values
	for k, v := range srv.Env {
		srv.Env[k] = maskSecret(config.ExpandEnv(v))
	}
	// Mask header values
	for k, v := range srv.Headers {
		srv.Headers[k] = maskSecret(config.ExpandEnv(v))
	}
	return srv
}

func redactIMAdapter(ad config.IMAdapterConfig) config.IMAdapterConfig {
	for k, v := range ad.Extra {
		if strVal, ok := v.(string); ok && config.LooksLikeSecretField(k) {
			ad.Extra[k] = maskSecret(config.ExpandEnv(strVal))
		}
	}
	return ad
}

// reloadProvider rebuilds the provider from current config and applies it
// to the running agent. Called after vendor/endpoint/model/api_key changes.
//
// Shared logic (all entry points):
//   - ResolveCurrentSelection → ApplyProviderToAgent (provider hot-swap)
//   - StartAsyncRelayModelLimitRefresh (background context window refresh)
//
// UI-specific logic is handled by the uiNotify callback (TUI: session sync,
// status bar refresh; Desktop: frontend state update).
func (a *configAccess) reloadProvider() {
	if a.agentInst == nil {
		debug.Log("config", "no agent set, skipping provider reload")
		return
	}

	resolved, prov, err := ResolveCurrentSelection(a.cfg)
	if err != nil {
		debug.Log("config", "provider reload failed: %v", err)
		return
	}

	ApplyProviderToAgent(a.agentInst, prov, resolved)
	StartAsyncRelayModelLimitRefresh(a.cfg, resolved, a.agentInst, nil)
	debug.Log("config", "provider reloaded: %s/%s/%s", resolved.VendorID, resolved.EndpointID, resolved.Model)

	if a.uiNotify != nil {
		a.uiNotify()
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// getHarnessConfig reads the project-level .ggcode/harness.yaml.
func (a *configAccess) getHarnessConfig(key string) (string, error) {
	harnessPath := filepath.Join(a.workingDir, ".ggcode", "harness.yaml")
	data, err := os.ReadFile(harnessPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "(no project harness config)", nil
		}
		return "", fmt.Errorf("reading harness config: %w", err)
	}
	return string(data), nil
}

// setHarnessConfig writes the project-level .ggcode/harness.yaml.
func (a *configAccess) setHarnessConfig(key, value string) error {
	dir := filepath.Join(a.workingDir, ".ggcode")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating .ggcode dir: %w", err)
	}
	harnessPath := filepath.Join(dir, "harness.yaml")
	return os.WriteFile(harnessPath, []byte(value), 0644)
}

// ============================================================================
// Model Discovery
// ============================================================================

// getEndpointModels returns the statically configured model list for an endpoint.
// Key format: vendors.<name>.endpoints.<ep>.models
func (a *configAccess) getEndpointModels(key string) (string, error) {
	// Parse: vendors.<vendor>.endpoints.<ep>.models
	parts := strings.SplitN(strings.TrimPrefix(key, "vendors."), ".", 3)
	if len(parts) < 3 || parts[1] != "endpoints" {
		return "", fmt.Errorf("invalid key format, expected vendors.<name>.endpoints.<ep>.models")
	}
	vendor := parts[0]
	epName := strings.TrimSuffix(parts[2], ".models")
	vc, ok := a.cfg.Vendors[vendor]
	if !ok {
		return "", fmt.Errorf("vendor %q not found", vendor)
	}
	ep, ok := vc.Endpoints[epName]
	if !ok {
		return "", fmt.Errorf("endpoint %q not found under vendor %q", epName, vendor)
	}
	if len(ep.Models) == 0 {
		return "(no models configured for this endpoint)", nil
	}
	b, _ := json.Marshal(ep.Models)
	return string(b), nil
}

// discoverModels calls the provider API to discover available models for an endpoint.
// Key format: vendors.<name>.endpoints.<ep>.discover_models
func (a *configAccess) discoverModels(key string) (string, error) {
	// Parse: vendors.<vendor>.endpoints.<ep>.discover_models
	path := strings.TrimSuffix(key, ".discover_models")
	vendorEpPath := strings.TrimPrefix(path, "vendors.")
	parts := strings.SplitN(vendorEpPath, ".", 3)
	if len(parts) < 3 || parts[1] != "endpoints" {
		return "", fmt.Errorf("invalid key format, expected vendors.<name>.endpoints.<ep>.discover_models")
	}
	vendor := parts[0]
	epName := parts[2]

	resolved, err := a.cfg.ResolveEndpoint(vendor, epName)
	if err != nil {
		return "", fmt.Errorf("cannot resolve endpoint %s/%s: %w", vendor, epName, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := provider.DiscoverModels(ctx, resolved)
	if err != nil {
		return "", fmt.Errorf("model discovery failed for %s/%s: %w", vendor, epName, err)
	}

	if len(models) == 0 {
		return "(no models discovered)", nil
	}

	b, _ := json.MarshalIndent(models, "", "  ")
	return string(b), nil
}
