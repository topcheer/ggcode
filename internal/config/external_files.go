package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"gopkg.in/yaml.v3"
)

// externalFilePath returns the path for a given external config section.
func externalFilePath(configDir, section string) string {
	return filepath.Join(configDir, section+".yaml")
}

// VendorsPath returns the path to vendors.yaml.
func VendorsPath(configDir string) string {
	return externalFilePath(configDir, "vendors")
}

// IMPath returns the path to im.yaml.
func IMPath(configDir string) string {
	return externalFilePath(configDir, "im")
}

// MCPServersPath returns the path to mcp_servers.yaml.
func MCPServersPath(configDir string) string {
	return externalFilePath(configDir, "mcp_servers")
}

// MCPDeletedPath returns the path to mcp_deleted.yaml (deletion tombstones).
func MCPDeletedPath(configDir string) string {
	return externalFilePath(configDir, "mcp_deleted")
}

// LoadMCPDeleted reads the tombstone list from mcp_deleted.yaml.
func LoadMCPDeleted(configDir string) []string {
	data, err := os.ReadFile(MCPDeletedPath(configDir))
	if err != nil {
		return nil
	}
	var names []string
	if err := yaml.Unmarshal(data, &names); err != nil {
		return nil
	}
	return names
}

// SaveMCPDeleted writes the tombstone list to mcp_deleted.yaml. An empty
// list removes the file.
func SaveMCPDeleted(configDir string, names []string) error {
	path := MCPDeletedPath(configDir)
	if len(names) == 0 {
		if fileExists(path) {
			return os.Remove(path)
		}
		return nil
	}
	out, err := yaml.Marshal(names)
	if err != nil {
		return err
	}
	return writeSecureConfigFile(path, out)
}

// loadExternalSections loads vendors, im, and mcp_servers from their respective
// standalone files if they exist. If the files don't exist but the data is
// present in the main config file, it auto-migrates them out.
//
// All three surfaces (daemon, TUI, desktop) use this through Load().
func loadExternalSections(cfg *Config, mainConfigPath string) {
	configDir := filepath.Dir(mainConfigPath)
	if configDir == "." {
		configDir = ConfigDir()
	}

	vendorsPath := VendorsPath(configDir)
	imPath := IMPath(configDir)
	mcpPath := MCPServersPath(configDir)

	// --- Vendors ---
	// #559 (Bug E): the external file merges FIELD-LEVEL into the in-memory
	// set (which mergeDefaultEndpoints already seeded with built-in vendors),
	// instead of replacing same-name vendors wholesale — so a user overriding
	// one field of a built-in vendor no longer deletes the other built-in
	// endpoints (probe: zai 6 endpoints collapsed to 1, switching back to a
	// deleted endpoint then failed Validate()).
	if fileExists(vendorsPath) {
		if v := loadVendorsFile(vendorsPath); v != nil {
			cfg.Vendors = mergeVendors(cfg.Vendors, v)
		}
	} else if hasMainSection(mainConfigPath, "vendors") {
		migrateSectionToExternal(mainConfigPath, configDir, "vendors")
	}

	// --- IM ---
	if fileExists(imPath) {
		if im := loadIMFile(imPath); im != nil {
			cfg.IM = *im
		}
	} else if hasMainSection(mainConfigPath, "im") {
		migrateSectionToExternal(mainConfigPath, configDir, "im")
	}

	// --- MCP Servers ---
	if fileExists(mcpPath) {
		if servers := loadMCPServersFile(mcpPath); servers != nil {
			cfg.MCPServers = servers
		}
	} else if hasMainSection(mainConfigPath, "mcp_servers") {
		migrateSectionToExternal(mainConfigPath, configDir, "mcp_servers")
	}

	// --- MCP deletion tombstones ---
	// Deleted names must survive config reloads so a Claude source file
	// rewritten behind our back cannot resurrect them via the merge.
	cfg.DeletedMCPServers = LoadMCPDeleted(configDir)
}

// saveExternalSections writes vendors, im, and mcp_servers to their respective
// standalone files. Called by Save() to keep external files in sync.
// configDir is the resolved external-files directory (unified with the
// vendors.yaml key migration in Save, fix #293); vendors is the vendor set to
// persist (Save passes the instance-only-filtered set).
func saveExternalSections(cfg *Config, configDir string, vendors map[string]VendorConfig) {
	if err := SaveVendors(configDir, vendors); err != nil {
		debug.Log("config", "failed to save vendors.yaml: %v", err)
	}
	if err := SaveIMConfig(configDir, &cfg.IM); err != nil {
		debug.Log("config", "failed to save im.yaml: %v", err)
	}
	if err := SaveMCPServers(configDir, cfg.MCPServers); err != nil {
		debug.Log("config", "failed to save mcp_servers.yaml: %v", err)
	}
}

// SaveVendors writes vendor definitions to vendors.yaml, stripping default
// vendors and applying flow-style formatting for models/tags arrays.
func SaveVendors(configDir string, vendors map[string]VendorConfig) error {
	path := VendorsPath(configDir)

	// Marshal vendors to raw map
	data, err := yaml.Marshal(vendors)
	if err != nil {
		return fmt.Errorf("marshaling vendors: %w", err)
	}
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing vendors: %w", err)
	}

	// Strip default vendors (don't bloat the file with 22 built-ins)
	defaults := DefaultConfig()
	defaultsData, _ := yaml.Marshal(defaults)
	defaultsRaw := map[string]interface{}{}
	yaml.Unmarshal(defaultsData, &defaultsRaw)
	if defaultVendors, ok := defaultsRaw["vendors"].(map[string]interface{}); ok {
		for vName, vVal := range raw {
			if defaultV, exists := defaultVendors[vName]; exists {
				if yamlEqual(vVal, defaultV) {
					delete(raw, vName)
				}
			}
		}
		if len(raw) == 0 {
			// All vendors are defaults — remove the file
			if fileExists(path) {
				os.Remove(path)
			}
			return nil
		}
	}

	// #608: Load() expands ${VAR} references in vendors.yaml into memory and
	// then auto-saves, which previously rewrote the file with the materialized
	// literal values — destroying the env references (data loss for anyone
	// managing secrets/URLs via environment variables). Before writing, restore
	// any ${VAR} leaf from the current on-disk file whose expansion equals the
	// in-memory value about to be written. If the in-memory value differs from
	// the expansion (user actually changed it), the new value is kept.
	if existingData, readErr := os.ReadFile(path); readErr == nil {
		existingRaw := map[string]interface{}{}
		if yaml.Unmarshal(existingData, &existingRaw) == nil {
			lookup := runtimeEnvLookup(nil)
			if restored, ok := restoreEnvRefs(existingRaw, raw, lookup).(map[string]interface{}); ok {
				raw = restored
			}
		}
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	// Wrap in {vendors: ...} so compactArraysInYAML finds the vendors key,
	// then strip the wrapper key from the output.
	wrappedData, _ := yaml.Marshal(map[string]interface{}{"vendors": raw})
	compacted := compactArraysInYAML(wrappedData)
	// Remove the first two lines ("vendors:\n") to unwrap.
	out = stripFirstYAMLKey(compacted)
	return writeSecureConfigFile(path, out)
}

// SaveIMConfig writes IM configuration to im.yaml.
func SaveIMConfig(configDir string, im *IMConfig) error {
	path := IMPath(configDir)

	data, err := yaml.Marshal(im)
	if err != nil {
		return fmt.Errorf("marshaling im config: %w", err)
	}
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing im config: %w", err)
	}
	cleanZeroYAMLValues(raw)
	if len(raw) == 0 {
		if fileExists(path) {
			os.Remove(path)
		}
		return nil
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	return writeSecureConfigFile(path, out)
}

// SaveMCPServers writes MCP server list to mcp_servers.yaml.
func SaveMCPServers(configDir string, servers []MCPServerConfig) error {
	path := MCPServersPath(configDir)

	if len(servers) == 0 {
		if fileExists(path) {
			os.Remove(path)
		}
		return nil
	}

	// Strip internal-only fields before persisting
	type persistMCP struct {
		Name              string            `yaml:"name" json:"name"`
		Type              string            `yaml:"type,omitempty" json:"type,omitempty"`
		Command           string            `yaml:"command,omitempty" json:"command,omitempty"`
		Args              []string          `yaml:"args,omitempty" json:"args,omitempty"`
		Env               map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
		URL               string            `yaml:"url,omitempty" json:"url,omitempty"`
		Headers           map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
		OAuthClientID     string            `yaml:"oauth_client_id,omitempty" json:"oauth_client_id,omitempty"`
		OAuthClientSecret string            `yaml:"oauth_client_secret,omitempty" json:"oauth_client_secret,omitempty"`
		ReadOnly          bool              `yaml:"read_only,omitempty" json:"read_only,omitempty"`
	}
	persist := make([]persistMCP, 0, len(servers))
	for _, s := range servers {
		persist = append(persist, persistMCP{
			Name:              s.Name,
			Type:              s.Type,
			Command:           s.Command,
			Args:              s.Args,
			Env:               s.Env,
			URL:               s.URL,
			Headers:           s.Headers,
			OAuthClientID:     s.OAuthClientID,
			OAuthClientSecret: s.OAuthClientSecret,
			ReadOnly:          s.ReadOnly,
		})
	}
	out, err := yaml.Marshal(persist)
	if err != nil {
		return fmt.Errorf("marshaling mcp servers: %w", err)
	}
	return writeSecureConfigFile(path, out)
}

// restoreEnvRefs walks an on-disk (unexpanded) tree and an outgoing
// (expanded) tree in parallel. At each string leaf, if the on-disk value
// contains a ${VAR} reference and expanding it with the runtime environment
// yields exactly the outgoing value, the unexpanded reference is returned so
// the caller can keep referencing the environment instead of materializing
// it (#608). Any mismatch (changed value, changed shape) keeps the outgoing
// value untouched.
func restoreEnvRefs(existing, out interface{}, lookup envLookupFunc) interface{} {
	switch ev := existing.(type) {
	case map[string]interface{}:
		if ov, ok := out.(map[string]interface{}); ok {
			for k, v := range ov {
				if evVal, present := ev[k]; present {
					ov[k] = restoreEnvRefs(evVal, v, lookup)
				}
			}
			return ov
		}
	case []interface{}:
		if ov, ok := out.([]interface{}); ok {
			for i := range ov {
				if i < len(ev) {
					ov[i] = restoreEnvRefs(ev[i], ov[i], lookup)
				}
			}
			return ov
		}
	case string:
		if outStr, ok := out.(string); ok && strings.Contains(ev, "${") {
			if ExpandEnvWithLookup(ev, lookup) == outStr {
				return ev
			}
		}
	}
	return out
}

// loadVendorsFile reads and parses a vendors.yaml file.
func loadVendorsFile(path string) map[string]VendorConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// Expand env vars before unmarshaling
	lookup := runtimeEnvLookup(nil)
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		debug.Log("config", "failed to parse %s: %v", path, err)
		return nil
	}
	expanded := ExpandEnvRecursiveWithLookup(raw, lookup)
	expandedData, _ := yaml.Marshal(expanded)

	var vendors map[string]VendorConfig
	if err := yaml.Unmarshal(expandedData, &vendors); err != nil {
		debug.Log("config", "failed to parse vendors from %s: %v", path, err)
		return nil
	}
	return vendors
}

// loadIMFile reads and parses an im.yaml file.
func loadIMFile(path string) *IMConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lookup := runtimeEnvLookup(nil)
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		debug.Log("config", "failed to parse %s: %v", path, err)
		return nil
	}
	expanded := ExpandEnvRecursiveWithLookup(raw, lookup)
	expandedData, _ := yaml.Marshal(expanded)

	var im IMConfig
	if err := yaml.Unmarshal(expandedData, &im); err != nil {
		debug.Log("config", "failed to parse im config from %s: %v", path, err)
		return nil
	}
	return &im
}

// loadMCPServersFile reads and parses an mcp_servers.yaml file.
func loadMCPServersFile(path string) []MCPServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lookup := runtimeEnvLookup(nil)

	// mcp_servers.yaml is a YAML array at top level (a sequence of maps).
	// Parse as []map[string]interface{} so env expansion works on each element.
	var rawList []map[string]interface{}
	if err := yaml.Unmarshal(data, &rawList); err != nil {
		debug.Log("config", "failed to parse mcp servers from %s: %v", path, err)
		return nil
	}
	for i, m := range rawList {
		rawList[i] = ExpandEnvRecursiveWithLookup(m, lookup)
	}
	expandedData, _ := yaml.Marshal(rawList)

	var servers []MCPServerConfig
	if err := yaml.Unmarshal(expandedData, &servers); err != nil {
		debug.Log("config", "failed to parse mcp servers from %s: %v", path, err)
		return nil
	}
	return servers
}

// LoadMCPServersPublic loads MCP server configs from the given file path.
// It is an exported wrapper around loadMCPServersFile for use by the
// hot-reload watcher.
func LoadMCPServersPublic(path string) []MCPServerConfig {
	return loadMCPServersFile(path)
}

// mergeVendors merges external vendors on top of in-memory vendors.
//
// #559 (Bug E): the merge is FIELD-LEVEL, not a wholesale replacement. When a
// user overrides a same-name built-in vendor in vendors.yaml (even a single
// field), built-in endpoints that the external definition does not mention are
// preserved, keeping mergeDefaultEndpoints' promise that "new built-in
// endpoints are available even when the user has an existing config" — load
// order in Load() is mergeDefaultEndpoints → loadExternalSections, so a
// whole-replace here would drop the built-in endpoints again (probe: zai
// collapsed 6 built-in endpoints to 1; switching back to a deleted endpoint
// then failed Validate()).
//
// Field-level precedence within an existing endpoint: non-zero external
// fields win; zero-value external fields keep the built-in value. Explicitly
// disabling a built-in endpoint remains possible by removing the whole vendor
// entry via a fully custom definition or a distinct vendor name.
func mergeVendors(base, external map[string]VendorConfig) map[string]VendorConfig {
	if external == nil {
		return base
	}
	if base == nil {
		base = make(map[string]VendorConfig)
	}
	for name, ext := range external {
		cur, exists := base[name]
		if !exists {
			base[name] = ext
			continue
		}
		// Vendor-level scalar merge: external non-zero fields win.
		if ext.DisplayName != "" {
			cur.DisplayName = ext.DisplayName
		}
		if ext.APIKey != "" {
			cur.APIKey = ext.APIKey
		}
		// Endpoint merge: external endpoints are merged into the built-in set;
		// built-in endpoints not mentioned by the external file are preserved.
		if cur.Endpoints == nil {
			cur.Endpoints = make(map[string]EndpointConfig)
		}
		for epName, extEP := range ext.Endpoints {
			curEP, epExists := cur.Endpoints[epName]
			if !epExists {
				cur.Endpoints[epName] = extEP
				continue
			}
			cur.Endpoints[epName] = mergeEndpointConfig(curEP, extEP)
		}
		base[name] = cur
	}
	return base
}

// mergeEndpointConfig merges an external endpoint definition on top of the
// built-in one. Non-zero external fields win; zero-value external fields keep
// the built-in value. This lets users override e.g. just the base_url while
// keeping built-in models/tags/protocol.
func mergeEndpointConfig(base, ext EndpointConfig) EndpointConfig {
	if ext.DisplayName != "" {
		base.DisplayName = ext.DisplayName
	}
	if ext.Protocol != "" {
		base.Protocol = ext.Protocol
	}
	if ext.BaseURL != "" {
		base.BaseURL = ext.BaseURL
	}
	if ext.AuthType != "" {
		base.AuthType = ext.AuthType
	}
	if ext.APIKey != "" {
		base.APIKey = ext.APIKey
	}
	if ext.DefaultModel != "" {
		base.DefaultModel = ext.DefaultModel
	}
	if ext.SelectedModel != "" {
		base.SelectedModel = ext.SelectedModel
	}
	if ext.ContextWindow > 0 {
		base.ContextWindow = ext.ContextWindow
	}
	if ext.MaxTokens > 0 {
		base.MaxTokens = ext.MaxTokens
	}
	if len(ext.Models) > 0 {
		base.Models = ext.Models
	}
	if len(ext.Tags) > 0 {
		base.Tags = ext.Tags
	}
	return base
}

// migrateSectionToExternal extracts a section from the main config file
// and writes it to a standalone external file, then removes it from the main file.
func migrateSectionToExternal(mainConfigPath, configDir, section string) {
	data, err := os.ReadFile(mainConfigPath)
	if err != nil {
		return
	}
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}

	sectionData, exists := raw[section]
	if !exists {
		return
	}

	// Write to external file
	var extPath string
	var outData []byte

	switch section {
	case "vendors":
		extPath = VendorsPath(configDir)
		// Strip default vendors before writing
		if vMap, ok := sectionData.(map[string]interface{}); ok {
			defaults := DefaultConfig()
			defaultsData, _ := yaml.Marshal(defaults)
			defaultsRaw := map[string]interface{}{}
			yaml.Unmarshal(defaultsData, &defaultsRaw)
			if defaultVendors, ok := defaultsRaw["vendors"].(map[string]interface{}); ok {
				for vName, vVal := range vMap {
					if defaultV, exists := defaultVendors[vName]; exists {
						if yamlEqual(vVal, defaultV) {
							delete(vMap, vName)
						}
					}
				}
				if len(vMap) == 0 {
					// All defaults — skip writing, just clean main file
					delete(raw, section)
					rewriteYAML(mainConfigPath, raw)
					return
				}
			}
			sectionData = vMap
		}
		out, err := yaml.Marshal(sectionData)
		if err != nil {
			return
		}
		_ = out
		// Wrap in {vendors: ...} so compactArraysInYAML finds the vendors key,
		// then strip wrapper.
		wrappedData, _ := yaml.Marshal(map[string]interface{}{"vendors": sectionData})
		compacted := compactArraysInYAML(wrappedData)
		outData = stripFirstYAMLKey(compacted)
	case "im":
		extPath = IMPath(configDir)
		out, err := yaml.Marshal(sectionData)
		if err != nil {
			return
		}
		outData = out
	case "mcp_servers":
		extPath = MCPServersPath(configDir)
		out, err := yaml.Marshal(sectionData)
		if err != nil {
			return
		}
		outData = out
	default:
		return
	}

	if err := writeSecureConfigFile(extPath, outData); err != nil {
		debug.Log("config", "failed to write %s during migration: %v", extPath, err)
		return
	}

	// Remove section from main config file
	delete(raw, section)
	if err := rewriteYAML(mainConfigPath, raw); err != nil {
		debug.Log("config", "failed to clean main config after migrating %s: %v", section, err)
	}
	debug.Log("config", "migrated %s from main config to %s", section, extPath)
}

// hasMainSection checks if the main config file contains a given top-level key.
func hasMainSection(mainConfigPath, section string) bool {
	data, err := os.ReadFile(mainConfigPath)
	if err != nil {
		return false
	}
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, exists := raw[section]
	return exists
}

// stripFirstYAMLKey removes the top-level YAML key wrapper.
// For example, "vendors:\n    myvendor:\n        ..." becomes "myvendor:\n    ...".
func stripFirstYAMLKey(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return data
	}
	// Find the first non-empty, non-comment line (the key line)
	keyIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		keyIdx = i
		break
	}
	if keyIdx < 0 {
		return data
	}
	// Check if this line ends with ":" (it's a YAML key)
	if !strings.HasSuffix(strings.TrimSpace(lines[keyIdx]), ":") {
		return data
	}
	// Remove the key line and dedent all subsequent lines by 4 spaces
	result := make([]string, 0, len(lines))
	result = append(result, lines[:keyIdx]...)
	for i := keyIdx + 1; i < len(lines); i++ {
		line := lines[i]
		// Remove exactly 4 leading spaces (common YAML indent)
		if strings.HasPrefix(line, "    ") {
			result = append(result, line[4:])
		} else if strings.HasPrefix(line, "\t\t\t\t") {
			result = append(result, line[4:])
		} else {
			result = append(result, line)
		}
	}
	return []byte(strings.Join(result, "\n"))
}

// fileExists checks if a file exists on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
