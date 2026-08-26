package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

// configAccess implements tool.ConfigAccess backed by *config.Config.
// It does NOT depend on any UI layer type.
type configAccess struct {
	cfgMu      sync.RWMutex // guards cfg pointer swaps and field refreshes (config hot-reload)
	cfg        *config.Config
	workingDir string
	agentInst  *agent.Agent // set after agent creation via SetAgent()
	uiNotify   func()       // optional UI refresh callback after provider changes
	hotReload  *ConfigHotReload
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
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	if a.cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	if v, ok, err := a.getCoreSectionKey(key); ok {
		return v, err
	}
	if v, ok, err := a.getAPIKeySectionKey(key); ok {
		return v, err
	}
	if v, ok, err := a.getVendorSectionKey(key); ok {
		return v, err
	}
	if v, ok, err := a.getMCPSectionKey(key); ok {
		return v, err
	}
	if v, ok, err := a.getIMSectionKey(key); ok {
		return v, err
	}
	if v, ok, err := a.getFallbackSectionKey(key); ok {
		return v, err
	}
	if v, ok, err := a.getA2ASectionKey(key); ok {
		return v, err
	}
	if v, ok, err := a.getKnightSectionKey(key); ok {
		return v, err
	}
	if v, ok, err := a.getRuntimeSectionKey(key); ok {
		return v, err
	}
	return "", fmt.Errorf("unknown config key: %q (use list=true to see all keys)", key)
}

// Per-section Get dispatchers. Each returns (value, handled, error);
// handled=false means the key belongs to another section. Section order and
// case order mirror the original single switch, so routing is unchanged.

func (a *configAccess) getCoreSectionKey(key string) (string, bool, error) {
	switch {
	case key == "vendor":
		return a.cfg.Vendor, true, nil
	case key == "endpoint":
		return a.cfg.Endpoint, true, nil
	case key == "model":
		return a.cfg.Model, true, nil
	case key == "language":
		return a.cfg.Language, true, nil
	case key == "default_mode":
		return a.cfg.DefaultMode, true, nil
	case key == "max_iterations":
		return strconv.Itoa(a.cfg.MaxIterations), true, nil
	case key == "extra_prompt":
		return a.cfg.ExtraPrompt, true, nil
	case key == "probe_context":
		return strconv.FormatBool(a.cfg.ProbeContext), true, nil
	}
	return "", false, nil
}

func (a *configAccess) getAPIKeySectionKey(key string) (string, bool, error) {
	switch {
	case key == "api_key":
		v, err := a.getAPIKey(a.cfg.Vendor, a.cfg.Endpoint)
		return v, true, err
	case strings.HasPrefix(key, "api_key."):
		v, err := a.getAPIKeyByPath(strings.TrimPrefix(key, "api_key."))
		return v, true, err
	case key == "api_keys":
		v, err := a.listAPIKeys()
		return v, true, err
	}
	return "", false, nil
}

func (a *configAccess) getVendorSectionKey(key string) (string, bool, error) {
	switch {
	case key == "vendors":
		v, err := a.listVendors()
		return v, true, err
	case strings.HasPrefix(key, "vendors.") && strings.HasSuffix(key, ".discover_models"):
		v, err := a.discoverModels(key)
		return v, true, err
	case strings.HasPrefix(key, "vendors.") && strings.HasSuffix(key, ".models"):
		v, err := a.getEndpointModels(key)
		return v, true, err
	case strings.HasPrefix(key, "vendors."):
		v, err := a.getVendorPath(strings.TrimPrefix(key, "vendors."))
		return v, true, err
	}
	return "", false, nil
}

func (a *configAccess) getMCPSectionKey(key string) (string, bool, error) {
	switch {
	case key == "mcp_servers":
		v, err := a.listMCPServers()
		return v, true, err
	case strings.HasPrefix(key, "mcp_servers."):
		v, err := a.getMCPServer(strings.TrimPrefix(key, "mcp_servers."))
		return v, true, err
	}
	return "", false, nil
}

func (a *configAccess) getIMSectionKey(key string) (string, bool, error) {
	switch {
	case key == "im.output_mode":
		return a.cfg.IM.OutputMode, true, nil
	case key == "im.adapters":
		v, err := a.listIMAdapters()
		return v, true, err
	case strings.HasPrefix(key, "im.adapters."):
		v, err := a.getIMAdapter(strings.TrimPrefix(key, "im.adapters."))
		return v, true, err
	}
	return "", false, nil
}

func (a *configAccess) getFallbackSectionKey(key string) (string, bool, error) {
	switch {
	case key == "fallback":
		b, err := json.Marshal(a.cfg.Fallback)
		if err != nil {
			return "", true, err
		}
		return string(b), true, nil
	case strings.HasPrefix(key, "fallback."):
		v, err := a.getFallbackField(a.cfg.Fallback, strings.TrimPrefix(key, "fallback."))
		return v, true, err
	case key == "fallbacks":
		b, err := json.Marshal(a.cfg.Fallbacks)
		if err != nil {
			return "", true, err
		}
		return string(b), true, nil
	case strings.HasPrefix(key, "fallbacks."):
		m := fallbacksIndexRe.FindStringSubmatch(key)
		if m == nil {
			return "", true, fmt.Errorf("invalid fallbacks key: %q (expected fallbacks.<N>[.<field>])", key)
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil || idx < 0 || idx >= len(a.cfg.Fallbacks) {
			return "", true, fmt.Errorf("fallbacks index out of range: %q (chain has %d entries)", key, len(a.cfg.Fallbacks))
		}
		v, err := a.getFallbackField(a.cfg.Fallbacks[idx], m[2])
		return v, true, err
	}
	return "", false, nil
}

func (a *configAccess) getA2ASectionKey(key string) (string, bool, error) {
	switch {
	case key == "a2a.disabled":
		return strconv.FormatBool(a.cfg.A2A.Disabled), true, nil
	case key == "a2a.host":
		return a.cfg.A2A.Host, true, nil
	case key == "a2a.port":
		return strconv.Itoa(a.cfg.A2A.Port), true, nil
	case strings.HasPrefix(key, "a2a.auth"):
		v, err := a.getA2AAuth(key)
		return v, true, err
	}
	return "", false, nil
}

func (a *configAccess) getKnightSectionKey(key string) (string, bool, error) {
	switch {
	case key == "knight.enabled":
		return strconv.FormatBool(a.cfg.KnightConfig.Enabled), true, nil
	case key == "knight.budget":
		return strconv.Itoa(a.cfg.KnightConfig.DailyTokenBudget), true, nil
	case key == "knight.idle_seconds":
		return strconv.Itoa(a.cfg.KnightConfig.IdleDelaySec), true, nil
	}
	return "", false, nil
}

func (a *configAccess) getRuntimeSectionKey(key string) (string, bool, error) {
	switch {
	case key == "allowed_dirs":
		b, _ := json.Marshal(a.cfg.AllowedDirs)
		return string(b), true, nil
	case key == "tool_permissions":
		v, err := a.getToolPermissions()
		return v, true, err
	case key == "scope":
		return a.cfg.GetSaveScope(), true, nil
	}
	return "", false, nil
}

// --- Set ---

func (a *configAccess) Set(key, value string) error {
	// Provider-affecting keys: probe before commit. The probe runs OUTSIDE
	// cfgMu (#957): these helpers snapshot the current selection under the
	// lock, release it for the network probe, then re-lock to re-validate and
	// commit. Holding cfgMu across the probe blocked every Get and the
	// hot-reload watcher for up to the probe timeout.
	switch {
	case key == "vendor", key == "endpoint", key == "model":
		return a.setWithProbe(key, value)
	case key == "api_key":
		return a.setAPIKeyWithProbe(value)
	case strings.HasPrefix(key, "api_key."):
		return a.setAPIKeyByPathWithProbe(strings.TrimPrefix(key, "api_key."), value)
	}

	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}

	// Non-provider keys: direct write under the lock, dispatched per section.
	if handled, err := a.setCoreSectionKey(key, value); handled {
		return err
	}
	if handled, err := a.setVendorSectionKey(key, value); handled {
		return err
	}
	if handled, err := a.setMCPSectionKey(key, value); handled {
		return err
	}
	if handled, err := a.setIMSectionKey(key, value); handled {
		return err
	}
	if handled, err := a.setFallbackSectionKey(key, value); handled {
		return err
	}
	if handled, err := a.setA2ASectionKey(key, value); handled {
		return err
	}
	if handled, err := a.setKnightSectionKey(key, value); handled {
		return err
	}
	if handled, err := a.setRuntimeSectionKey(key, value); handled {
		return err
	}
	return fmt.Errorf("unknown config key: %q", key)
}

// Per-section Set dispatchers (called with cfgMu held). Each returns
// (handled, error); handled=false means the key belongs to another section.
// Section order and case order mirror the original single switch.

func (a *configAccess) setCoreSectionKey(key, value string) (bool, error) {
	switch {
	case key == "language":
		return true, a.cfg.SaveLanguagePreference(value)
	case key == "default_mode":
		return true, a.cfg.SaveDefaultModePreference(value)
	case key == "max_iterations":
		n, err := strconv.Atoi(value)
		if err != nil {
			return true, fmt.Errorf("invalid max_iterations: %w", err)
		}
		a.cfg.MaxIterations = n
		return true, a.saveAndPatch("max_iterations", value)
	case key == "extra_prompt":
		a.cfg.ExtraPrompt = value
		return true, a.saveAndPatch("extra_prompt", value)
	case key == "probe_context":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return true, fmt.Errorf("invalid probe_context: %w", err)
		}
		a.cfg.ProbeContext = b
		return true, a.saveAndPatch("probe_context", value)
	}
	return false, nil
}

func (a *configAccess) setVendorSectionKey(key, value string) (bool, error) {
	switch {
	case key == "vendors":
		return true, fmt.Errorf("use 'vendors.<name>' to manage vendors")
	case strings.HasPrefix(key, "vendors."):
		return true, a.setVendorPath(strings.TrimPrefix(key, "vendors."), value)
	}
	return false, nil
}

func (a *configAccess) setMCPSectionKey(key, value string) (bool, error) {
	switch {
	case strings.HasPrefix(key, "mcp_servers."):
		return true, a.setMCPServer(strings.TrimPrefix(key, "mcp_servers."), value)
	}
	return false, nil
}

func (a *configAccess) setIMSectionKey(key, value string) (bool, error) {
	switch {
	case key == "im.output_mode":
		a.cfg.IM.OutputMode = value
		return true, a.saveAndPatch("im.output_mode", value)
	case strings.HasPrefix(key, "im.adapters."):
		return true, a.setIMAdapterPath(strings.TrimPrefix(key, "im.adapters."), value)
	}
	return false, nil
}

func (a *configAccess) setFallbackSectionKey(key, value string) (bool, error) {
	switch {
	case strings.HasPrefix(key, "fallback."):
		return true, a.setFallbackField(&a.cfg.Fallback, strings.TrimPrefix(key, "fallback."), value)
	case key == "fallbacks":
		return true, a.setFallbacksList(value)
	// Exact match first: a plain HasPrefix("fallbacks.") case would shadow
	// "fallbacks.append" ("append" fails the <N> regex) and reject it with
	// the very format the error message advertises (#958).
	case key == "fallbacks.append":
		return true, a.appendFallbackEntry(value)
	case strings.HasPrefix(key, "fallbacks."):
		m := fallbacksIndexRe.FindStringSubmatch(key)
		if m == nil {
			return true, fmt.Errorf("invalid fallbacks key: %q (expected fallbacks.<N>[.<field>] or fallbacks.append)", key)
		}
		return true, a.setFallbacksIndexed(m[1], m[2], value)
	}
	return false, nil
}

func (a *configAccess) setA2ASectionKey(key, value string) (bool, error) {
	switch {
	case key == "a2a.disabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return true, fmt.Errorf("invalid a2a.disabled: %w", err)
		}
		a.cfg.A2A.Disabled = b
		return true, a.saveAndPatch("a2a.disabled", value)
	case key == "a2a.host":
		a.cfg.A2A.Host = value
		return true, a.saveAndPatch("a2a.host", value)
	case key == "a2a.port":
		n, err := strconv.Atoi(value)
		if err != nil {
			return true, fmt.Errorf("invalid a2a.port: %w", err)
		}
		a.cfg.A2A.Port = n
		return true, a.saveAndPatch("a2a.port", value)
	case strings.HasPrefix(key, "a2a.auth"):
		return true, a.setA2AAuth(key, value)
	}
	return false, nil
}

func (a *configAccess) setKnightSectionKey(key, value string) (bool, error) {
	switch {
	case key == "knight.enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return true, fmt.Errorf("invalid knight.enabled: %w", err)
		}
		return true, a.cfg.SaveKnightEnabled(b)
	case key == "knight.budget":
		n, err := strconv.Atoi(value)
		if err != nil {
			return true, fmt.Errorf("invalid knight.budget: %w", err)
		}
		a.cfg.KnightConfig.DailyTokenBudget = n
		return true, a.saveAndPatch("knight.daily_token_budget", value)
	case key == "knight.idle_seconds":
		n, err := strconv.Atoi(value)
		if err != nil {
			return true, fmt.Errorf("invalid knight.idle_seconds: %w", err)
		}
		a.cfg.KnightConfig.IdleDelaySec = n
		return true, a.saveAndPatch("knight.idle_delay_sec", value)
	}
	return false, nil
}

func (a *configAccess) setRuntimeSectionKey(key, value string) (bool, error) {
	switch {
	case key == "allowed_dirs":
		var dirs []string
		if err := json.Unmarshal([]byte(value), &dirs); err != nil {
			return true, fmt.Errorf("invalid allowed_dirs (expected JSON array): %w", err)
		}
		a.cfg.AllowedDirs = dirs
		return true, a.saveAndPatch("allowed_dirs", value)
	case key == "tool_permissions":
		return true, a.setToolPermissions(value)
	case key == "scope":
		return true, a.cfg.SetSaveScope(value)
	}
	return false, nil
}

// --- List ---

func (a *configAccess) List(section string) (string, error) {
	// Whole-section read under RLock: the hot-reload watcher refreshes scalar
	// fields (MaxIterations, KnightConfig, ...) on this same Config object
	// under the write lock, so an unlocked List is a data race (#957).
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
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
	case "runtime":
		sb.WriteString(a.listSectionRuntime())
	default:
		return "", fmt.Errorf("unknown section %q (valid: core, api_key, vendors, mcp, im, a2a, knight, runtime)", section)
	}

	return sb.String(), nil
}

// --- Delete ---

func (a *configAccess) Delete(key string) error {
	// Lock the whole operation: RemoveMCPServer/RemoveIMAdapter mutate the
	// same maps/slices the hot-reload watcher and locked Set paths touch, so
	// an unlocked Delete is a concurrent-write race (#957).
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	switch {
	case strings.HasPrefix(key, "mcp_servers."):
		name := strings.TrimPrefix(key, "mcp_servers.")
		if !a.cfg.RemoveMCPServer(name) {
			return fmt.Errorf("MCP server %q not found", name)
		}
		return a.cfg.SaveMCPServersScoped(a.cfg.GetSaveScope())
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

// probeProviderFn is the seam the Set provider-key paths probe through.
// Package-level var so tests can stub the network round-trip.
var probeProviderFn = probeProvider

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
// #957: the network probe runs OUTSIDE cfgMu so concurrent Get calls and
// the hot-reload watcher are not blocked for the probe duration. TOCTOU
// safety: the pre-probe Vendor/Endpoint/Model snapshot is re-validated under
// the re-acquired lock before committing; a concurrent change aborts the
// switch instead of clobbering it.
func (a *configAccess) setWithProbe(key, value string) error {
	a.cfgMu.Lock()
	if a.cfg == nil {
		a.cfgMu.Unlock()
		return fmt.Errorf("config is nil")
	}
	oldVendor, oldEndpoint, oldModel := a.cfg.Vendor, a.cfg.Endpoint, a.cfg.Model
	newVendor, newEndpoint, newModel := oldVendor, oldEndpoint, oldModel
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
	a.cfgMu.Unlock()
	if err != nil {
		return fmt.Errorf("cannot resolve target %s/%s/%s: %w", newVendor, newEndpoint, newModel, err)
	}

	// Probe — outside the lock.
	if err := probeProviderFn(testResolved); err != nil {
		a.cfgMu.RLock()
		curVendor, curEndpoint, curModel := "", "", ""
		if a.cfg != nil {
			curVendor, curEndpoint, curModel = a.cfg.Vendor, a.cfg.Endpoint, a.cfg.Model
		}
		a.cfgMu.RUnlock()
		return fmt.Errorf("refusing to switch to %s/%s/%s: probe failed: %w.\nCurrent provider is unchanged (%s/%s/%s).",
			newVendor, newEndpoint, newModel, err,
			curVendor, curEndpoint, curModel)
	}

	// Probe passed — re-validate and commit under the lock.
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if a.cfg.Vendor != oldVendor || a.cfg.Endpoint != oldEndpoint || a.cfg.Model != oldModel {
		return fmt.Errorf("provider selection changed concurrently during probe (%s/%s/%s -> %s/%s/%s); retry the switch",
			oldVendor, oldEndpoint, oldModel, a.cfg.Vendor, a.cfg.Endpoint, a.cfg.Model)
	}
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
	// #957: snapshot + resolve under the lock, probe outside, re-validate on
	// commit (same pattern as setWithProbe).
	a.cfgMu.Lock()
	if a.cfg == nil {
		a.cfgMu.Unlock()
		return fmt.Errorf("config is nil")
	}
	vendor, endpoint := a.cfg.Vendor, a.cfg.Endpoint
	// Resolve with current key first to get the endpoint config
	testResolved, err := a.cfg.ResolveEndpoint(vendor, endpoint)
	a.cfgMu.Unlock()
	if err != nil {
		return fmt.Errorf("cannot resolve endpoint: %w", err)
	}
	// Override with new key for probe
	testResolved.APIKey = value

	if err := probeProviderFn(testResolved); err != nil {
		return fmt.Errorf("refusing to set api_key: probe failed: %w.\nCurrent key is unchanged.", err)
	}

	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if a.cfg.Vendor != vendor || a.cfg.Endpoint != endpoint {
		return fmt.Errorf("active endpoint changed concurrently during probe (%s/%s -> %s/%s); retry",
			vendor, endpoint, a.cfg.Vendor, a.cfg.Endpoint)
	}
	err = a.cfg.SetEndpointAPIKey(vendor, endpoint, value, false)
	if err == nil {
		a.reloadProvider()
	}
	return err
}

func (a *configAccess) setAPIKeyByPathWithProbe(path, value string) error {
	// #957: probe runs outside cfgMu (snapshot under lock, network unlocked,
	// re-lock to commit), same pattern as setWithProbe.
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 1 {
		return a.setVendorAPIKeyWithProbe(parts[0], value)
	}
	return a.setEndpointAPIKeyWithProbe(parts[0], parts[1], value)
}

// setVendorAPIKeyWithProbe probes and commits a vendor-level API key.
func (a *configAccess) setVendorAPIKeyWithProbe(vendor, value string) error {
	// #875: the session's endpoint name belongs to the session's VENDOR —
	// probing vendor X with it either rejects legal keys (endpoint not
	// configured for X) or probes a same-named wrong endpoint. Prefer an
	// endpoint of THIS vendor: the session's endpoint if it belongs to the
	// vendor, else the sole endpoint if unambiguous.
	a.cfgMu.Lock()
	if a.cfg == nil {
		a.cfgMu.Unlock()
		return fmt.Errorf("config is nil")
	}
	probeEndpoint := a.cfg.Endpoint
	if vc, ok := a.cfg.Vendors[vendor]; ok {
		if _, ok := vc.Endpoints[probeEndpoint]; !ok {
			if len(vc.Endpoints) == 1 {
				for name := range vc.Endpoints {
					probeEndpoint = name
				}
			} else if probeEndpoint != "" {
				// Ambiguous: can't guess which endpoint to probe.
				a.cfgMu.Unlock()
				return fmt.Errorf("vendor %s has multiple endpoints and the current session endpoint %q does not belong to it; set the key at endpoint level (vendors.%s.<endpoint>.api_key) so the probe targets the right endpoint", vendor, probeEndpoint, vendor)
			}
		}
	}
	testResolved, err := a.cfg.ResolveEndpointSelection(vendor, probeEndpoint, a.cfg.Model)
	a.cfgMu.Unlock()
	if err != nil {
		return fmt.Errorf("cannot resolve: %w", err)
	}
	testResolved.APIKey = value
	if err := probeProviderFn(testResolved); err != nil {
		return fmt.Errorf("refusing to set api_key for vendor %s: probe failed: %w", vendor, err)
	}

	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
	}
	err = a.cfg.SetVendorAPIKey(vendor, value)
	if err == nil {
		a.reloadProvider()
	}
	return err
}

// setEndpointAPIKeyWithProbe probes and commits an endpoint-level API key.
func (a *configAccess) setEndpointAPIKeyWithProbe(vendor, endpoint, value string) error {
	a.cfgMu.Lock()
	if a.cfg == nil {
		a.cfgMu.Unlock()
		return fmt.Errorf("config is nil")
	}
	testResolved, err := a.cfg.ResolveEndpoint(vendor, endpoint)
	a.cfgMu.Unlock()
	if err != nil {
		return fmt.Errorf("cannot resolve: %w", err)
	}
	testResolved.APIKey = value
	if err := probeProviderFn(testResolved); err != nil {
		return fmt.Errorf("refusing to set api_key for %s/%s: probe failed: %w", vendor, endpoint, err)
	}

	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.cfg == nil {
		return fmt.Errorf("config is nil")
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
	default:
		return "", fmt.Errorf("unknown a2a auth key: %q", key)
	}
}

func (a *configAccess) setA2AAuth(key, value string) error {
	switch key {
	case "a2a.auth.api_key":
		return a.setA2ASecret("api_key", value)
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
	return fmt.Sprintf("== Core ==\n  vendor: %s\n  endpoint: %s\n  model: %s\n  language: %s\n  default_mode: %s\n  max_iterations: %d\n  session_token_budget: %d\n  extra_prompt: %s\n  probe_context: %v\n",
		a.cfg.Vendor, a.cfg.Endpoint, a.cfg.Model, a.cfg.Language,
		a.cfg.DefaultMode, a.cfg.MaxIterations, a.cfg.SessionTokenBudget,
		util.Truncate(a.cfg.ExtraPrompt, 80), a.cfg.ProbeContext)
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
		sb.WriteString(fmt.Sprintf("  %s: %s\n", srv.Name, util.Truncate(cmd, 60)))
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
	return fmt.Sprintf("== A2A ==\n  disabled: %v\n  host: %s\n  port: %d\n  auth: %s\n",
		a.cfg.A2A.Disabled, a.cfg.A2A.Host, a.cfg.A2A.Port,
		strings.Join(methods, "+"))
}

func (a *configAccess) listSectionKnight() string {
	return fmt.Sprintf("== Knight ==\n  enabled: %v\n  budget: %d\n  idle_seconds: %d\n",
		a.cfg.KnightConfig.Enabled, a.cfg.KnightConfig.DailyTokenBudget, a.cfg.KnightConfig.IdleDelaySec)
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

// saveAndPatch persists a config change via SaveScoped.
// Save() uses merge semantics: existing file content is preserved and only
// non-zero fields from the current config are overlaid. This prevents
// concurrent processes from clobbering each other's changes.
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
	// Guard on rune count, not byte length: same multibyte divergence as
	// maskSecret in cmd/ggcode (runes 3-7 panic, 8 runes print unmasked). #745
	r := []rune(s)
	if len(r) <= 8 {
		return "****"
	}
	return string(r[:4]) + strings.Repeat("*", len(r)-8) + string(r[len(r)-4:])
}

// redactStringMap returns a masked copy of m. The source map is never
// modified: writing masks into the struct-copied map shared with the live
// config polluted it — a later Save would persist "${VAR} (set, ****)"
// garbage over the real env reference (#956).
func redactStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = maskSecret(config.ExpandEnv(v))
	}
	return out
}

func redactMCPServer(srv config.MCPServerConfig) config.MCPServerConfig {
	// Mask env and header values into fresh maps, leaving the live config's
	// maps untouched (#956).
	srv.Env = redactStringMap(srv.Env)
	srv.Headers = redactStringMap(srv.Headers)
	return srv
}

func redactIMAdapter(ad config.IMAdapterConfig) config.IMAdapterConfig {
	if ad.Extra == nil {
		return ad
	}
	// Mask secret-looking extra fields into a fresh map, leaving the live
	// config's map untouched (#956).
	extra := make(map[string]interface{}, len(ad.Extra))
	for k, v := range ad.Extra {
		if strVal, ok := v.(string); ok && config.LooksLikeSecretField(k) {
			extra[k] = maskSecret(config.ExpandEnv(strVal))
		} else {
			extra[k] = v
		}
	}
	ad.Extra = extra
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
	ApplySessionTokenBudget(a.agentInst, a.cfg)
	ApplyToolCallBudget(a.agentInst, a.cfg)
	ApplySessionTimeout(a.agentInst, a.cfg, false)
	StartAsyncRelayModelLimitRefresh(a.cfg, resolved, a.agentInst, nil)
	debug.Log("config", "provider reloaded: %s/%s/%s", resolved.VendorID, resolved.EndpointID, resolved.Model)

	if a.uiNotify != nil {
		a.uiNotify()
	}
}

// truncate was removed in #1030: its byte-sliced s[:maxLen] could split a
// multi-byte rune and inject invalid UTF-8 into config tool output. Both call
// sites now use the rune-safe util.Truncate (same family as #1029/#745).

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

	// #957: discovery is a network call made while holding only a read lock;
	// keep the timeout short so pending writers (Set, hot-reload) are not
	// starved behind it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

// --- Provider fallback chain access ---
//
// Keys:
//   fallback                    read whole legacy entry (JSON)
//   fallback.<field>            read/write legacy entry field (enabled/vendor/endpoint/model)
//   fallbacks                   read whole chain / write as JSON array (replaces)
//   fallbacks.<N>               read chain entry N (JSON)
//   fallbacks.<N>.<field>       write chain entry N field
//   fallbacks.append            append a JSON entry to the chain

// fallbacksIndexRe matches "fallbacks.<N>" and "fallbacks.<N>.<field>".
var fallbacksIndexRe = regexp.MustCompile(`^fallbacks\.(\d+)(?:\.([a-z_]+))?$`)

// getFallbackField reads one field of a fallback entry.
func (a *configAccess) getFallbackField(fb config.FallbackConfig, field string) (string, error) {
	switch field {
	case "enabled":
		return strconv.FormatBool(fb.Enabled), nil
	case "vendor":
		return fb.Vendor, nil
	case "endpoint":
		return fb.Endpoint, nil
	case "model":
		return fb.Model, nil
	case "":
		b, err := json.Marshal(fb)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return "", fmt.Errorf("unknown fallback field: %q (enabled/vendor/endpoint/model)", field)
	}
}

// setFallbackField writes one field of the legacy single fallback entry.
func (a *configAccess) setFallbackField(fb *config.FallbackConfig, field, value string) error {
	switch field {
	case "enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid enabled: %w", err)
		}
		fb.Enabled = b
	case "vendor":
		fb.Vendor = value
	case "endpoint":
		fb.Endpoint = value
	case "model":
		fb.Model = value
	default:
		return fmt.Errorf("unknown fallback field: %q (enabled/vendor/endpoint/model)", field)
	}
	return a.saveAndPatch("fallback."+field, value)
}

// setFallbacksList replaces the whole fallbacks list from a JSON array.
func (a *configAccess) setFallbacksList(value string) error {
	var list []config.FallbackConfig
	if err := json.Unmarshal([]byte(value), &list); err != nil {
		return fmt.Errorf("fallbacks must be a JSON array of entries: %w", err)
	}
	a.cfg.Fallbacks = list
	return a.saveAndPatch("fallbacks", value)
}

// setFallbacksIndexed writes one field of chain entry idxStr.
func (a *configAccess) setFallbacksIndexed(idxStr, field, value string) error {
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 || idx >= len(a.cfg.Fallbacks) {
		return fmt.Errorf("fallbacks index out of range: %q (chain has %d entries)", idxStr, len(a.cfg.Fallbacks))
	}
	fb := &a.cfg.Fallbacks[idx]
	switch field {
	case "enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid enabled: %w", err)
		}
		fb.Enabled = b
	case "vendor":
		fb.Vendor = value
	case "endpoint":
		fb.Endpoint = value
	case "model":
		fb.Model = value
	case "":
		return fmt.Errorf("fallbacks.<N> writes are not supported; use fallbacks.<N>.<field>")
	default:
		return fmt.Errorf("unknown fallback field: %q (enabled/vendor/endpoint/model)", field)
	}
	return a.saveAndPatch("fallbacks."+idxStr+"."+field, value)
}

// appendFallbackEntry appends one JSON entry to the fallbacks chain.
func (a *configAccess) appendFallbackEntry(value string) error {
	var fb config.FallbackConfig
	if err := json.Unmarshal([]byte(value), &fb); err != nil {
		return fmt.Errorf("fallbacks.append must be a JSON entry object: %w", err)
	}
	a.cfg.Fallbacks = append(a.cfg.Fallbacks, fb)
	return a.saveAndPatch("fallbacks.append", value)
}
