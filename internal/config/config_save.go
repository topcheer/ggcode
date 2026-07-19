package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
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
//
// Merge semantics: if the target file already exists, the current config is
// overlaid onto the existing file content via deep-merge. This prevents one
// process from clobbering fields that another process added concurrently.
// Only when the file does NOT exist (first write) is a full overwrite used.
//
// If an instance config was merged at load time (globalSnap is set), instance-
// only fields are excluded so they never leak into the global file.
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

	// 1. Marshal current config into a raw map.
	currentData, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	currentRaw := map[string]interface{}{}
	if err := yaml.Unmarshal(currentData, &currentRaw); err != nil {
		return fmt.Errorf("parsing current config: %w", err)
	}

	// 2. Exclude fields that came from instance config merge so they don't
	//    leak into the global file.
	if c.globalSnap != nil && len(c.instanceFields) > 0 {
		for key := range c.instanceFields {
			delete(currentRaw, key)
		}
	}

	// 3. Remove zero-value entries from the current map. This ensures that
	//    un-set fields don't overwrite real values that exist in the file.
	//    Only non-zero values participate in the merge overlay.
	cleanZeroYAMLValues(currentRaw)

	// 4. Build the data to write: merge onto existing file, or full write.
	var data []byte
	existingData, readErr := os.ReadFile(c.FilePath)
	if readErr == nil && len(existingData) > 0 {
		// File exists — deep-merge current onto existing.
		existingRaw := map[string]interface{}{}
		if yamlErr := yaml.Unmarshal(existingData, &existingRaw); yamlErr == nil {
			deepMergeYAMLMaps(existingRaw, currentRaw)
			merged, marshalErr := yaml.Marshal(existingRaw)
			if marshalErr != nil {
				return fmt.Errorf("marshaling merged config: %w", marshalErr)
			}
			data = stripDefaultsFromYAML(merged)
		} else {
			// Existing file is unparseable — fall back to full write.
			data = stripDefaultsFromYAML(currentData)
		}
	} else {
		// File doesn't exist or is empty — full write.
		data = stripDefaultsFromYAML(currentData)
	}

	if err := writeSecureConfigFile(c.FilePath, data); err != nil {
		return err
	}
	// Re-migrate any plaintext secrets that yaml.Marshal produced.
	if migrated, migrateErr := MigratePlaintextAPIKeys(c.FilePath); migrateErr != nil {
		debug.Log("config", "Save: post-save migration error: %v", migrateErr)
	} else if len(migrated) > 0 {
		if err := recompactConfigFile(c.FilePath); err != nil {
			debug.Log("config", "Save: recompact error: %v", err)
		}
	}
	return nil
}

// deepMergeYAMLMaps merges src onto dst in-place. For each key:
//   - If both dst and src have the key with map values, merge recursively.
//   - Otherwise (scalars, slices, type mismatches), src overwrites dst.
//
// This ensures that nested maps (like vendors, im.adapters, knight) are
// merged field-by-field, while slices (like mcp_servers) are replaced
// wholesale (honoring additions and removals from the current process).
func deepMergeYAMLMaps(dst, src map[string]interface{}) {
	for key, srcVal := range src {
		if dstVal, exists := dst[key]; exists {
			srcMap, srcIsMap := srcVal.(map[string]interface{})
			dstMap, dstIsMap := dstVal.(map[string]interface{})
			if srcIsMap && dstIsMap {
				deepMergeYAMLMaps(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcVal
	}
}

// cleanZeroYAMLValues recursively removes zero-value entries from a YAML map
// so they don't overwrite real values during merge. Removed zero values:
//   - nil
//   - "" (empty string)
//   - empty []interface{}
//   - empty map[string]interface{} (after recursive cleaning)
//
// Integers (0) and booleans (false) are NOT cleaned because they can be
// All int fields in Config use 0 as the "not set / default" sentinel
// (max_iterations=0 means unlimited, max_tokens=0 means unlimited, etc.),
// so cleaning 0 is semantically correct: it means "preserve the existing
// file value rather than overwriting it with the default".
func cleanZeroYAMLValues(m map[string]interface{}) {
	for k, v := range m {
		switch val := v.(type) {
		case nil:
			delete(m, k)
		case string:
			if val == "" {
				delete(m, k)
			}
		case int:
			if val == 0 {
				delete(m, k)
			}
		case int64:
			if val == 0 {
				delete(m, k)
			}
		case float64:
			if val == 0 {
				delete(m, k)
			}
		case []interface{}:
			if len(val) == 0 {
				delete(m, k)
			}
		case map[string]interface{}:
			cleanZeroYAMLValues(val)
			if len(val) == 0 {
				delete(m, k)
			}
		}
	}
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
	if err := writeSecureConfigFile(fp, updated); err != nil {
		return err
	}

	// Migrate plaintext API keys if saving to a real file.
	if c.saveScope == "instance" && c.instanceDir != "" {
		hash := filepath.Base(c.instanceDir)
		if _, migrateErr := MigrateInstancePlaintextAPIKeys(fp, hash); migrateErr != nil {
			debug.Log("config", "patchConfigFile: instance migration error: %v", migrateErr)
		}
	} else {
		if _, migrateErr := MigratePlaintextAPIKeys(fp); migrateErr != nil {
			debug.Log("config", "patchConfigFile: migration error: %v", migrateErr)
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
		return false
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

// SaveKnightEnabled persists the knight.enabled setting to the config file.
func (c *Config) SaveKnightEnabled(enabled bool) error {
	if err := c.patchConfigFile(func(raw map[string]interface{}) {
		knightMap, _ := raw["knight"].(map[string]interface{})
		if knightMap == nil {
			knightMap = map[string]interface{}{}
		}
		knightMap["enabled"] = enabled
		raw["knight"] = knightMap
	}); err != nil {
		return err
	}
	c.KnightConfig.Enabled = enabled
	c.KnightConfig.SetEnabledExplicitly()
	return nil
}

// SaveA2AEnabled persists the a2a.disabled setting to the config file.
// A2A is enabled by default; setting enabled=false writes disabled=true.
func (c *Config) SaveA2AEnabled(enabled bool) error {
	if err := c.patchConfigFile(func(raw map[string]interface{}) {
		a2aMap, _ := raw["a2a"].(map[string]interface{})
		if a2aMap == nil {
			a2aMap = map[string]interface{}{}
		}
		a2aMap["disabled"] = !enabled
		raw["a2a"] = a2aMap
	}); err != nil {
		return err
	}
	c.A2A.Disabled = !enabled
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
	// Use patchConfigFile so the target list is explicitly written,
	// avoiding omitempty issues with Save()-based merge.
	targetData, _ := yaml.Marshal(target)
	targetMap := map[string]interface{}{}
	yaml.Unmarshal(targetData, &targetMap)
	return c.PatchIMAdapter(adapterName, func(a map[string]interface{}) {
		targets, _ := a["targets"].([]interface{})
		// Replace existing target with same ID, or append.
		found := false
		for i, t := range targets {
			if tm, ok := t.(map[string]interface{}); ok {
				if id, _ := tm["id"].(string); strings.EqualFold(strings.TrimSpace(id), target.ID) {
					targets[i] = targetMap
					found = true
					break
				}
			}
		}
		if !found {
			targets = append(targets, targetMap)
		}
		a["targets"] = targets
	})
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
	// Use patchConfigFile so all fields (including enabled=false) are written.
	adapterData, _ := yaml.Marshal(adapter)
	adapterMap := map[string]interface{}{}
	yaml.Unmarshal(adapterData, &adapterMap)
	return c.patchConfigFile(func(raw map[string]interface{}) {
		imRaw, _ := raw["im"].(map[string]interface{})
		if imRaw == nil {
			imRaw = map[string]interface{}{}
		}
		adaptersRaw, _ := imRaw["adapters"].(map[string]interface{})
		if adaptersRaw == nil {
			adaptersRaw = map[string]interface{}{}
		}
		adaptersRaw[name] = adapterMap
		imRaw["adapters"] = adaptersRaw
		raw["im"] = imRaw
	})
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
	// Use patchConfigFile to explicitly delete the adapter from the file,
	// avoiding merge semantics that would preserve it.
	return c.patchConfigFile(func(raw map[string]interface{}) {
		if imRaw, ok := raw["im"].(map[string]interface{}); ok {
			if adaptersRaw, ok := imRaw["adapters"].(map[string]interface{}); ok {
				delete(adaptersRaw, name)
			}
		}
	})
}

// SetIMAdapterEnabled toggles the enabled state of an IM adapter.
// Uses patchConfigFile so enabled=false is explicitly written (the
// yaml tag has omitempty, so Save()-based merge would omit it).
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
	return c.PatchIMAdapter(name, func(a map[string]interface{}) {
		a["enabled"] = enabled
	})
}

// SetIMAdapterExtra sets a single key in the adapter's Extra map.
// Uses patchConfigFile for correct merge semantics.
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
	return c.PatchIMAdapter(name, func(a map[string]interface{}) {
		extra, _ := a["extra"].(map[string]interface{})
		if extra == nil {
			extra = map[string]interface{}{}
		}
		extra[key] = value
		a["extra"] = extra
	})
}

// PatchIMAdapter navigates the raw YAML map to reach a single IM adapter
// and applies a patch function to it. This ensures explicit field writes
// (including zero values like enabled=false) are correctly persisted,
// avoiding omitempty issues that arise with Save()-based merge.
func (c *Config) PatchIMAdapter(name string, patch func(adapter map[string]interface{})) error {
	return c.patchConfigFile(func(raw map[string]interface{}) {
		imRaw, _ := raw["im"].(map[string]interface{})
		if imRaw == nil {
			imRaw = map[string]interface{}{}
		}
		adaptersRaw, _ := imRaw["adapters"].(map[string]interface{})
		if adaptersRaw == nil {
			adaptersRaw = map[string]interface{}{}
		}
		adapterRaw, _ := adaptersRaw[name].(map[string]interface{})
		if adapterRaw == nil {
			adapterRaw = map[string]interface{}{}
		}
		patch(adapterRaw)
		adaptersRaw[name] = adapterRaw
		imRaw["adapters"] = adaptersRaw
		raw["im"] = imRaw
	})
}

// recompactConfigFile reads a config file, strips defaults, and rewrites it.
func recompactConfigFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	data = stripDefaultsFromYAML(data)
	return writeSecureConfigFile(path, data)
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
