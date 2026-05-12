package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
	"gopkg.in/yaml.v3"
)

// effectiveFilePath returns the file path that should be used for config I/O.
// When a saveScope is set to "instance", it returns the instance config path.
// Otherwise it returns the global config path (c.FilePath).
func (c *Config) effectiveFilePath(saveScope string) string {
	if saveScope == "instance" && c.instancePath != "" {
		return c.instancePath
	}
	return c.FilePath
}

// Save persists the config to its configured file path (global config).
// If an instance config was merged at load time (globalSnap is set), only
// fields that existed in the global config are written back. Instance-only
// fields are never leaked into the global file.
func (c *Config) Save() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.FilePath) == "" {
		return fmt.Errorf("config file path is empty")
	}
	unlock := lockConfigFile(c.FilePath)
	defer unlock()
	if err := c.Validate(); err != nil {
		return err
	}

	var data []byte
	var err error
	if c.globalSnap != nil && len(c.instanceFields) > 0 {
		// Instance config was merged. Write back only global fields.
		// Strategy: serialize globalSnap as the base, then overlay with
		// current Config values for fields that globalSnap already had set.
		data, err = c.marshalGlobalOnly()
		if err != nil {
			return fmt.Errorf("marshaling global-only config: %w", err)
		}
	} else {
		data, err = yaml.Marshal(c)
		if err != nil {
			return fmt.Errorf("marshaling config: %w", err)
		}
	}
	// Strip fields identical to DefaultConfig() to keep the file minimal.
	data = stripDefaultsFromYAML(data)
	if err := os.MkdirAll(filepath.Dir(c.FilePath), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := util.AtomicWriteFile(c.FilePath, data, 0644); err != nil {
		return err
	}
	// Re-migrate any plaintext secrets that yaml.Marshal produced.
	if migrated, migrateErr := MigratePlaintextAPIKeys(c.FilePath); migrateErr != nil {
		debug.Log("config", "Save: post-save migration error: %v", migrateErr)
	} else if len(migrated) > 0 {
		debug.Log("config", "Save: re-migrated %d plaintext secret(s)", len(migrated))
		recompactConfigFile(c.FilePath)
	}
	return nil
}

// marshalGlobalOnly serializes the config for writing to the global file,
// excluding fields that were filled in by instance config merge.
//
// Strategy:
//  1. Serialize current Config as raw map
//  2. Remove any top-level keys that are in instanceFields
//  3. Return the cleaned YAML
func (c *Config) marshalGlobalOnly() ([]byte, error) {
	currentData, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}

	if len(c.instanceFields) == 0 {
		return currentData, nil
	}

	currentRaw := map[string]interface{}{}
	if err := yaml.Unmarshal(currentData, &currentRaw); err != nil {
		return nil, err
	}

	// Remove fields that came from instance config.
	for key := range c.instanceFields {
		delete(currentRaw, key)
	}

	return yaml.Marshal(currentRaw)
}

// patchConfigFile reads the effective config file (global or instance depending
// on saveScope), applies a patch function to the raw YAML map, and writes it back.
// This is the common implementation for Save*Preference methods.
func (c *Config) patchConfigFile(patch func(raw map[string]interface{})) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	fp := c.effectiveFilePath(c.saveScope)
	if strings.TrimSpace(fp) == "" {
		return fmt.Errorf("config file path is empty")
	}
	unlock := lockConfigFile(fp)
	defer unlock()

	raw := map[string]interface{}{}
	data, err := os.ReadFile(fp)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading config %s: %w", fp, err)
		}
		// File doesn't exist yet — start with empty map.
	} else if len(data) > 0 {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parsing config %s: %w", fp, err)
		}
	}

	patch(raw)

	updated, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := util.AtomicWriteFile(fp, updated, 0644); err != nil {
		return err
	}

	// Migrate plaintext API keys if saving to a real file.
	if c.saveScope == "instance" && c.instanceDir != "" {
		hash := filepath.Base(c.instanceDir)
		if migrated, migrateErr := MigrateInstancePlaintextAPIKeys(fp, hash); migrateErr != nil {
			debug.Log("config", "patchConfigFile: instance migration error: %v", migrateErr)
		} else if len(migrated) > 0 {
			debug.Log("config", "patchConfigFile: migrated %d instance secret(s)", len(migrated))
		}
	} else {
		if migrated, migrateErr := MigratePlaintextAPIKeys(fp); migrateErr != nil {
			debug.Log("config", "patchConfigFile: migration error: %v", migrateErr)
		} else if len(migrated) > 0 {
			debug.Log("config", "patchConfigFile: migrated %d plaintext secret(s)", len(migrated))
		}
	}

	return nil
}

func (c *Config) SaveLanguagePreference(lang string) error {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return fmt.Errorf("language must not be empty")
	}
	if err := c.patchConfigFile(func(raw map[string]interface{}) {
		raw["language"] = lang
	}); err != nil {
		return err
	}
	c.Language = lang
	c.FirstRun = false
	return nil
}

// SaveImpersonation persists impersonation settings to the config file.
func (c *Config) SaveImpersonation(imp ImpersonationConfig) error {
	return c.patchConfigFile(func(raw map[string]interface{}) {
		if imp.Preset == "" && imp.CustomVersion == "" && len(imp.CustomHeaders) == 0 {
			delete(raw, "impersonation")
		} else {
			impMap := map[string]interface{}{}
			if imp.Preset != "" {
				impMap["preset"] = imp.Preset
			}
			if imp.CustomVersion != "" {
				impMap["custom_version"] = imp.CustomVersion
			}
			if len(imp.CustomHeaders) > 0 {
				impMap["custom_headers"] = imp.CustomHeaders
			}
			raw["impersonation"] = impMap
		}
	})
}

func (c *Config) SidebarVisible() bool {
	if c == nil || c.UI.SidebarVisible == nil {
		return true
	}
	return *c.UI.SidebarVisible
}

func (c *Config) SaveSidebarPreference(visible bool) error {
	if err := c.patchConfigFile(func(raw map[string]interface{}) {
		uiRaw, _ := raw["ui"].(map[string]interface{})
		if uiRaw == nil {
			uiRaw = map[string]interface{}{}
		}
		uiRaw["sidebar_visible"] = visible
		raw["ui"] = uiRaw
	}); err != nil {
		return err
	}
	c.UI.SidebarVisible = boolPtr(visible)
	c.FirstRun = false
	return nil
}

func (c *Config) SaveDefaultModePreference(mode string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "supervised", "plan", "auto", "bypass", "autopilot":
	default:
		return fmt.Errorf("default_mode %q must be one of supervised, plan, auto, bypass, autopilot", mode)
	}
	if err := c.patchConfigFile(func(raw map[string]interface{}) {
		raw["default_mode"] = mode
	}); err != nil {
		return err
	}
	c.DefaultMode = mode
	c.FirstRun = false
	return nil
}

func (c *Config) AddIMTarget(adapterName string, target IMTargetConfig) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	adapterName = strings.TrimSpace(adapterName)
	if adapterName == "" {
		return fmt.Errorf("adapter name is required")
	}
	if c.IM.Adapters == nil {
		return fmt.Errorf("IM adapters are not configured")
	}
	adapter, ok := c.IM.Adapters[adapterName]
	if !ok {
		return fmt.Errorf("IM adapter %q is not configured", adapterName)
	}
	target.ID = strings.TrimSpace(target.ID)
	target.Label = strings.TrimSpace(target.Label)
	target.Channel = strings.TrimSpace(target.Channel)
	target.Thread = strings.TrimSpace(target.Thread)
	if target.ID == "" {
		return fmt.Errorf("target id is required")
	}
	if target.Channel == "" {
		return fmt.Errorf("channel is required")
	}
	replaced := false
	for i := range adapter.Targets {
		if strings.EqualFold(strings.TrimSpace(adapter.Targets[i].ID), target.ID) {
			adapter.Targets[i] = target
			replaced = true
			break
		}
	}
	if !replaced {
		adapter.Targets = append(adapter.Targets, target)
	}
	c.IM.Adapters[adapterName] = adapter
	return c.SaveScoped(c.saveScope)
}

func (c *Config) AddIMAdapter(name string, adapter IMAdapterConfig) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("adapter name is required")
	}
	adapter.Platform = strings.TrimSpace(adapter.Platform)
	if adapter.Platform == "" {
		return fmt.Errorf("adapter platform is required")
	}
	if c.IM.Adapters == nil {
		c.IM.Adapters = make(map[string]IMAdapterConfig)
	}
	if _, exists := c.IM.Adapters[name]; exists {
		return fmt.Errorf("IM adapter %q already exists", name)
	}
	c.IM.Adapters[name] = adapter
	return c.SaveScoped(c.saveScope)
}

// RemoveIMAdapter removes an IM adapter from the configuration.
func (c *Config) RemoveIMAdapter(name string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("adapter name is required")
	}
	if c.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	if _, exists := c.IM.Adapters[name]; !exists {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	delete(c.IM.Adapters, name)
	return c.SaveScoped(c.saveScope)
}

// SetIMAdapterEnabled toggles the enabled state of an IM adapter.
func (c *Config) SetIMAdapterEnabled(name string, enabled bool) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if c.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	adapter, ok := c.IM.Adapters[name]
	if !ok {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	adapter.Enabled = enabled
	c.IM.Adapters[name] = adapter
	return c.SaveScoped(c.saveScope)
}

// SetIMAdapterExtra sets a single key in the adapter's Extra map.
func (c *Config) SetIMAdapterExtra(name, key, value string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("key is required")
	}
	if c.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	adapter, ok := c.IM.Adapters[name]
	if !ok {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	if adapter.Extra == nil {
		adapter.Extra = make(map[string]interface{})
	}
	adapter.Extra[key] = value
	c.IM.Adapters[name] = adapter
	return c.SaveScoped(c.saveScope)
}

// recompactConfigFile reads a config file, strips defaults, and rewrites it.
func recompactConfigFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	data = stripDefaultsFromYAML(data)
	return os.WriteFile(path, data, 0644)
}

// stripDefaultsFromYAML removes top-level YAML entries that are identical to
// DefaultConfig(). This prevents 22 built-in vendors from bloating the config.
func stripDefaultsFromYAML(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	raw := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return data
	}
	defaults := DefaultConfig()
	defaultsData, err := yaml.Marshal(defaults)
	if err != nil {
		return data
	}
	defaultsRaw := map[string]interface{}{}
	if err := yaml.Unmarshal(defaultsData, &defaultsRaw); err != nil {
		return data
	}
	if vendorsRaw, ok := raw["vendors"].(map[string]interface{}); ok {
		if defaultVendors, ok := defaultsRaw["vendors"].(map[string]interface{}); ok {
			for vName, vVal := range vendorsRaw {
				if defaultV, exists := defaultVendors[vName]; exists {
					if yamlEqual(vVal, defaultV) {
						delete(vendorsRaw, vName)
					}
				}
			}
			if len(vendorsRaw) == 0 {
				delete(raw, "vendors")
			}
		}
	}
	for _, key := range []string{"mcp_servers", "plugins", "tool_permissions"} {
		if val, ok := raw[key]; ok {
			if defaultVal, ok := defaultsRaw[key]; ok {
				if yamlEqual(val, defaultVal) {
					delete(raw, key)
				}
			}
		}
	}
	if ad, ok := raw["allowed_dirs"].([]interface{}); ok {
		if dad, ok := defaultsRaw["allowed_dirs"].([]interface{}); ok {
			if yamlEqual(ad, dad) {
				delete(raw, "allowed_dirs")
			}
		}
	}
	cleaned, err := yaml.Marshal(raw)
	if err != nil {
		return data
	}
	return compactArraysInYAML(cleaned)
}

func compactArraysInYAML(data []byte) []byte {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return data
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return data
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return data
	}
	compactKeys := map[string]bool{"models": true, "tags": true}
	for i := 0; i < len(root.Content); i += 2 {
		key := root.Content[i]
		val := root.Content[i+1]
		if key.Value == "vendors" && val.Kind == yaml.MappingNode {
			for j := 0; j < len(val.Content); j += 2 {
				vendorVal := val.Content[j+1]
				if vendorVal.Kind != yaml.MappingNode {
					continue
				}
				for k := 0; k < len(vendorVal.Content); k += 2 {
					if vendorVal.Content[k].Value == "endpoints" && vendorVal.Content[k+1].Kind == yaml.MappingNode {
						endpoints := vendorVal.Content[k+1]
						for m := 0; m < len(endpoints.Content); m += 2 {
							epVal := endpoints.Content[m+1]
							if epVal.Kind != yaml.MappingNode {
								continue
							}
							for n := 0; n < len(epVal.Content); n += 2 {
								if compactKeys[epVal.Content[n].Value] && epVal.Content[n+1].Kind == yaml.SequenceNode {
									epVal.Content[n+1].Style = yaml.FlowStyle
								}
							}
						}
					}
				}
			}
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return data
	}
	return out
}

func yamlEqual(a, b interface{}) bool {
	aData, errA := yaml.Marshal(a)
	bData, errB := yaml.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(aData) == string(bData)
}
