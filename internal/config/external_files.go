package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"gopkg.in/yaml.v3"
)

func externalFilePath(configDir, section string) string {
	return filepath.Join(configDir, section+".yaml")
}

func VendorsPath(configDir string) string {
	return externalFilePath(configDir, "vendors")
}

func IMPath(configDir string) string {
	return externalFilePath(configDir, "im")
}

func MCPServersPath(configDir string) string {
	return externalFilePath(configDir, "mcp_servers")
}

func MCPDeletedPath(configDir string) string {
	return externalFilePath(configDir, "mcp_deleted")
}

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

func loadExternalSections(cfg *Config, mainConfigPath string) {
	configDir := filepath.Dir(mainConfigPath)
	if configDir == "." {
		configDir = ConfigDir()
	}

	vendorsPath := VendorsPath(configDir)
	imPath := IMPath(configDir)
	mcpPath := MCPServersPath(configDir)

	if fileExists(vendorsPath) {
		if v := loadVendorsFile(vendorsPath); v != nil {
			cfg.Vendors = mergeVendors(cfg.Vendors, v)
		}
	} else if hasMainSection(mainConfigPath, "vendors") {
		migrateSectionToExternal(mainConfigPath, configDir, "vendors")
	}

	if fileExists(imPath) {
		if im := loadIMFile(imPath); im != nil {

			cfg.IM = mergeIMExternal(cfg.IM, *im)
		}
	} else if hasMainSection(mainConfigPath, "im") {
		migrateSectionToExternal(mainConfigPath, configDir, "im")
	}

	if fileExists(mcpPath) {
		if servers := loadMCPServersFile(mcpPath); servers != nil {
			cfg.MCPServers = servers
		}
	} else if hasMainSection(mainConfigPath, "mcp_servers") {
		migrateSectionToExternal(mainConfigPath, configDir, "mcp_servers")
	}

	cfg.DeletedMCPServers = LoadMCPDeleted(configDir)
}

func saveExternalSections(cfg *Config, configDir string, vendors map[string]VendorConfig) error {
	var firstErr error
	if err := SaveVendors(configDir, vendors); err != nil {
		debug.Log("config", "failed to save vendors.yaml: %v", err)
		firstErr = err
	}
	if err := SaveIMConfig(configDir, &cfg.IM); err != nil {
		debug.Log("config", "failed to save im.yaml: %v", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	if err := SaveMCPServers(configDir, cfg.MCPServers); err != nil {
		debug.Log("config", "failed to save mcp_servers.yaml: %v", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func SaveVendors(configDir string, vendors map[string]VendorConfig) error {
	path := VendorsPath(configDir)

	data, err := yaml.Marshal(vendors)
	if err != nil {
		return fmt.Errorf("marshaling vendors: %w", err)
	}
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing vendors: %w", err)
	}

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

			if fileExists(path) {
				os.Remove(path)
			}
			return nil
		}
	}

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

	wrappedData, _ := yaml.Marshal(map[string]interface{}{"vendors": raw})
	compacted := compactArraysInYAML(wrappedData)

	out = stripFirstYAMLKey(compacted)
	return writeSecureConfigFile(path, out)
}

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
	return writeSecureConfigFile(path, out)
}

func SaveMCPServers(configDir string, servers []MCPServerConfig) error {
	path := MCPServersPath(configDir)

	if len(servers) == 0 {
		if fileExists(path) {
			os.Remove(path)
		}
		return nil
	}

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

	if existingData, readErr := os.ReadFile(path); readErr == nil {
		existingList := []interface{}{}
		if yaml.Unmarshal(existingData, &existingList) == nil {
			outList := []interface{}{}
			if yaml.Unmarshal(out, &outList) == nil {

				aligned := alignByName(existingList, outList)
				lookup := runtimeEnvLookup(nil)
				restored := restoreEnvRefs(aligned, outList, lookup)
				if re, rerr := yaml.Marshal(restored); rerr == nil {
					out = re
				}
			}
		}
	}
	return writeSecureConfigFile(path, out)
}

func alignByName(existing, out []interface{}) []interface{} {
	idxByName := map[string]int{}
	for i, el := range existing {
		m, ok := el.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		if _, dup := idxByName[name]; !dup {
			idxByName[name] = i
		}
	}
	if len(idxByName) == 0 {
		return existing
	}
	used := map[int]bool{}
	aligned := make([]interface{}, 0, len(out))
	fallback := 0
	for _, el := range out {
		m, ok := el.(map[string]interface{})
		matched := false
		if ok {
			name, _ := m["name"].(string)
			if i, hit := idxByName[name]; hit && !used[i] {
				aligned = append(aligned, existing[i])
				used[i] = true
				matched = true
			}
		}
		if !matched {
			for fallback < len(existing) && used[fallback] {
				fallback++
			}
			if fallback < len(existing) {
				aligned = append(aligned, existing[fallback])
				used[fallback] = true
				fallback++
			}

		}
	}
	return aligned
}

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

func loadVendorsFile(path string) map[string]VendorConfig {
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

	var vendors map[string]VendorConfig
	if err := yaml.Unmarshal(expandedData, &vendors); err != nil {
		debug.Log("config", "failed to parse vendors from %s: %v", path, err)
		return nil
	}
	return vendors
}

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

func loadMCPServersFile(path string) []MCPServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lookup := runtimeEnvLookup(nil)

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

func LoadMCPServersPublic(path string) []MCPServerConfig {
	return loadMCPServersFile(path)
}

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

		if ext.DisplayName != "" {
			cur.DisplayName = ext.DisplayName
		}
		if ext.APIKey != "" {
			cur.APIKey = ext.APIKey
		}

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

func mergeIMExternal(base, ext IMConfig) IMConfig {
	if ext.ActiveSessionPolicy != "" {
		base.ActiveSessionPolicy = ext.ActiveSessionPolicy
	}
	if ext.RequireLocalSession != nil {
		base.RequireLocalSession = ext.RequireLocalSession
	}
	if ext.OutputMode != "" {
		base.OutputMode = ext.OutputMode
	}
	if ext.Enabled {
		base.Enabled = ext.Enabled
	}
	base.Streaming = mergeIMStreamingConfig(base.Streaming, ext.Streaming)
	base.STT = mergeIMSTTConfig(base.STT, ext.STT)
	if ext.Adapters != nil {
		if base.Adapters == nil {
			base.Adapters = make(map[string]IMAdapterConfig)
		}
		for name, extA := range ext.Adapters {
			cur, exists := base.Adapters[name]
			if !exists {
				base.Adapters[name] = extA
				continue
			}
			base.Adapters[name] = mergeIMAdapterConfig(cur, extA)
		}
	}
	return base
}

func mergeIMStreamingConfig(base, ext IMStreamingConfig) IMStreamingConfig {
	if ext.Enabled {
		base.Enabled = ext.Enabled
	}
	if ext.Transport != "" {
		base.Transport = ext.Transport
	}
	if ext.EditIntervalSec > 0 {
		base.EditIntervalSec = ext.EditIntervalSec
	}
	if ext.BufferThreshold > 0 {
		base.BufferThreshold = ext.BufferThreshold
	}
	if ext.Cursor != "" {
		base.Cursor = ext.Cursor
	}
	return base
}

func mergeIMSTTConfig(base, ext IMSTTConfig) IMSTTConfig {
	if ext.Provider != "" {
		base.Provider = ext.Provider
	}
	if ext.BaseURL != "" {
		base.BaseURL = ext.BaseURL
	}
	if ext.APIKey != "" {
		base.APIKey = ext.APIKey
	}
	if ext.Model != "" {
		base.Model = ext.Model
	}
	if ext.LocalModel != "" {
		base.LocalModel = ext.LocalModel
	}
	return base
}

func mergeIMAdapterConfig(base, ext IMAdapterConfig) IMAdapterConfig {
	if ext.Enabled {
		base.Enabled = ext.Enabled
	}
	if ext.Platform != "" {
		base.Platform = ext.Platform
	}
	if ext.Transport != "" {
		base.Transport = ext.Transport
	}
	if ext.Command != "" {
		base.Command = ext.Command
	}
	if len(ext.Args) > 0 {
		base.Args = ext.Args
	}
	if ext.Env != nil {
		if base.Env == nil {
			base.Env = make(map[string]string)
		}
		for k, v := range ext.Env {
			base.Env[k] = v
		}
	}
	if len(ext.AllowFrom) > 0 {
		base.AllowFrom = ext.AllowFrom
	}
	if ext.OutputMode != "" {
		base.OutputMode = ext.OutputMode
	}
	if len(ext.Targets) > 0 {
		base.Targets = ext.Targets
	}
	if ext.Extra != nil {
		if base.Extra == nil {
			base.Extra = make(map[string]interface{})
		}
		for k, v := range ext.Extra {
			base.Extra[k] = v
		}
	}
	return base
}

func migrateSectionToExternal(mainConfigPath, configDir, section string) {

	unlock := lockConfigFile(mainConfigPath)
	defer unlock()

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

	var extPath string
	var outData []byte

	switch section {
	case "vendors":
		extPath = VendorsPath(configDir)

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

	delete(raw, section)
	if err := rewriteYAML(mainConfigPath, raw); err != nil {
		debug.Log("config", "failed to clean main config after migrating %s: %v", section, err)
	}
	debug.Log("config", "migrated %s from main config to %s", section, extPath)
}

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

func stripFirstYAMLKey(data []byte) []byte {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return data
	}

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

	if !strings.HasSuffix(strings.TrimSpace(lines[keyIdx]), ":") {
		return data
	}

	result := make([]string, 0, len(lines))
	result = append(result, lines[:keyIdx]...)
	for i := keyIdx + 1; i < len(lines); i++ {
		line := lines[i]

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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
