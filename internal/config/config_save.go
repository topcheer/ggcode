package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"gopkg.in/yaml.v3"
)

func (c *Config) effectiveFilePath(saveScope string) string {
	if saveScope == "instance" && c.instancePath != "" {
		return c.instancePath
	}
	return c.FilePath
}

func (c *Config) Save() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.FilePath) == "" {
		return fmt.Errorf("config file path is empty")
	}

	if err := GuardRealHomePath(c.FilePath, "Save()"); err != nil {
		return err
	}
	unlock := lockConfigFile(c.FilePath)
	defer unlock()
	if err := c.Validate(); err != nil {
		return err
	}

	extDir := filepath.Dir(c.FilePath)
	if extDir == "." {
		extDir = ConfigDir()
	}

	vendorsToSave := c.Vendors
	if c.globalSnap != nil {
		vendorsToSave = globalOnlyVendors(c.Vendors, c.globalSnap.Vendors)
	}

	if err := saveExternalSections(c, extDir, vendorsToSave); err != nil {
		return fmt.Errorf("saving external sections: %w", err)
	}

	currentData, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	currentRaw := map[string]interface{}{}
	if err := yaml.Unmarshal(currentData, &currentRaw); err != nil {
		return fmt.Errorf("parsing current config: %w", err)
	}

	delete(currentRaw, "vendors")
	delete(currentRaw, "im")
	delete(currentRaw, "mcp_servers")

	if c.globalSnap != nil && len(c.instanceFields) > 0 {
		for key := range c.instanceFields {
			delete(currentRaw, key)
		}
	}

	clearedInMemory := collectEmptyStringPaths(currentRaw)

	clearedZeros := endpointZeroClearPaths(c)
	cleanZeroYAMLValues(currentRaw)

	var data []byte
	existingData, readErr := os.ReadFile(c.FilePath)

	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("reading existing config %s: %w", c.FilePath, readErr)
	}
	if readErr == nil && len(existingData) > 0 {

		existingRaw := map[string]interface{}{}
		if yamlErr := yaml.Unmarshal(existingData, &existingRaw); yamlErr == nil {
			deepMergeYAMLMaps(existingRaw, currentRaw)

			applyClearedTombstones(existingRaw, c.diskStrSnap, clearedInMemory)

			applyZeroNumberClears(existingRaw, clearedZeros)
			merged, marshalErr := yaml.Marshal(existingRaw)
			if marshalErr != nil {
				return fmt.Errorf("marshaling merged config: %w", marshalErr)
			}
			data = stripDefaultsFromYAML(merged)
		} else {

			fallbackData, marshalErr := yaml.Marshal(currentRaw)
			if marshalErr != nil {
				return fmt.Errorf("marshaling config for fallback write: %w", marshalErr)
			}
			data = stripDefaultsFromYAML(fallbackData)
		}
	} else {

		fullData, marshalErr := yaml.Marshal(currentRaw)
		if marshalErr != nil {
			return fmt.Errorf("marshaling config for first write: %w", marshalErr)
		}
		data = stripDefaultsFromYAML(fullData)
	}

	if err := writeSecureConfigFile(c.FilePath, data); err != nil {
		return err
	}

	if migrated, migrateErr := MigratePlaintextAPIKeys(c.FilePath); migrateErr != nil {
		return fmt.Errorf("config saved, but migrating plaintext API keys out of %s failed (they remain readable on disk): %w", c.FilePath, migrateErr)
	} else if len(migrated) > 0 {
		if err := recompactConfigFile(c.FilePath); err != nil {
			debug.Log("config", "Save: recompact error: %v", err)
		}
	}

	if migrated, migrateErr := MigrateVendorsFilePlaintextAPIKeys(VendorsPath(extDir), ""); migrateErr != nil {

		return fmt.Errorf("config saved, but migrating plaintext API keys out of vendors.yaml failed (they remain readable on disk): %w", migrateErr)
	} else if len(migrated) > 0 {
		debug.Log("config", "Save: migrated %d plaintext API keys out of vendors.yaml", len(migrated))
	}
	return nil
}

func globalOnlyVendors(merged, globalSnapVendors map[string]VendorConfig) map[string]VendorConfig {
	if globalSnapVendors == nil {
		return merged
	}
	out := make(map[string]VendorConfig, len(merged))
	for name := range merged {
		if gvc, isGlobal := globalSnapVendors[name]; isGlobal {

			out[name] = gvc
		}
	}
	return out
}

func deepMergeYAMLMaps(dst, src map[string]interface{}) {
	for key, srcVal := range src {

		if srcVal == nil {
			delete(dst, key)
			continue
		}
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

func snapshotDiskStringKeys(m map[string]interface{}) map[string]string {
	out := map[string]string{}
	var walk func(prefix []string, cur map[string]interface{})
	walk = func(prefix []string, cur map[string]interface{}) {
		for k, v := range cur {
			switch val := v.(type) {
			case string:
				if val != "" {
					path := strings.Join(append(append([]string{}, prefix...), k), ".")
					out[path] = val
				}
			case map[string]interface{}:
				walk(append(append([]string{}, prefix...), k), val)
			}
		}
	}
	walk(nil, m)
	return out
}

func endpointZeroClearPaths(c *Config) map[string]bool {
	out := map[string]bool{}
	if c == nil {
		return out
	}
	for vName, vc := range c.Vendors {
		for eName, ep := range vc.Endpoints {
			if ep.ContextWindow == 0 {
				out["vendors."+vName+".endpoints."+eName+".context_window"] = true
			}
			if ep.MaxTokens == 0 {
				out["vendors."+vName+".endpoints."+eName+".max_tokens"] = true
			}
		}
	}
	return out
}

func applyZeroNumberClears(existingRaw map[string]interface{}, zeros map[string]bool) {
	for path := range zeros {
		if _, exists := yamlPathLookup(existingRaw, path); exists {
			yamlPathDelete(existingRaw, strings.Split(path, "."))
		}
	}
}

func collectEmptyStringPaths(m map[string]interface{}) map[string]bool {
	out := map[string]bool{}
	var walk func(prefix []string, cur map[string]interface{})
	walk = func(prefix []string, cur map[string]interface{}) {
		for k, v := range cur {
			switch val := v.(type) {
			case string:
				if val == "" {
					path := strings.Join(append(append([]string{}, prefix...), k), ".")
					out[path] = true
				}
			case map[string]interface{}:
				walk(append(append([]string{}, prefix...), k), val)
			}
		}
	}
	walk(nil, m)
	return out
}

func applyClearedTombstones(existingRaw map[string]interface{}, snap map[string]string, cleared map[string]bool) {
	if len(snap) == 0 || len(cleared) == 0 {
		return
	}
	for path := range cleared {
		loadedVal, wasOnDisk := snap[path]
		if !wasOnDisk || strings.Contains(loadedVal, "${") {
			continue
		}
		curVal, exists := yamlPathLookup(existingRaw, path)
		if !exists {
			continue
		}
		curStr, isStr := curVal.(string)
		if !isStr || curStr != loadedVal {
			continue
		}
		yamlPathDelete(existingRaw, strings.Split(path, "."))
	}
}

func yamlPathLookup(m map[string]interface{}, path string) (interface{}, bool) {
	parts := strings.Split(path, ".")
	var cur interface{} = m
	for _, p := range parts {
		cm, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		v, exists := cm[p]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func yamlPathDelete(m map[string]interface{}, parts []string) {
	if len(parts) == 0 {
		return
	}
	if len(parts) > 1 {
		if next, ok := m[parts[0]].(map[string]interface{}); ok {
			yamlPathDelete(next, parts[1:])
			if len(next) == 0 {
				delete(m, parts[0])
			}
		}
		return
	}
	delete(m, parts[0])
}

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

	case "supervised", "auto", "bypass", "autopilot":
	default:
		return fmt.Errorf("default_mode %q must be supervised or bypass (legacy aliases auto/autopilot normalize; plan is tool-internal)", mode)
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
	c.imAdaptersMu.Lock()
	defer c.imAdaptersMu.Unlock()
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

	targetData, _ := yaml.Marshal(target)
	targetMap := map[string]interface{}{}
	yaml.Unmarshal(targetData, &targetMap)
	return c.patchIMAdapterUnlocked(adapterName, func(a map[string]interface{}) {
		targets, _ := a["targets"].([]interface{})

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
	c.imAdaptersMu.Lock()
	defer c.imAdaptersMu.Unlock()
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

	c.IM.Enabled = true

	adapterData, _ := yaml.Marshal(adapter)
	adapterMap := map[string]interface{}{}
	yaml.Unmarshal(adapterData, &adapterMap)
	if c.saveScope == "instance" {
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
			imRaw["enabled"] = true
			raw["im"] = imRaw
		})
	}
	return c.patchExternalFile("im", func(raw map[string]interface{}) {
		adaptersRaw, _ := raw["adapters"].(map[string]interface{})
		if adaptersRaw == nil {
			adaptersRaw = map[string]interface{}{}
		}
		adaptersRaw[name] = adapterMap
		raw["adapters"] = adaptersRaw
		raw["enabled"] = true
	})
}

func (c *Config) RemoveIMAdapter(name string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	c.imAdaptersMu.Lock()
	defer c.imAdaptersMu.Unlock()
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
	if c.saveScope == "instance" {
		return c.patchConfigFile(func(raw map[string]interface{}) {
			if imRaw, ok := raw["im"].(map[string]interface{}); ok {
				if adaptersRaw, ok := imRaw["adapters"].(map[string]interface{}); ok {
					delete(adaptersRaw, name)
				}
			}
		})
	}
	return c.patchExternalFile("im", func(raw map[string]interface{}) {
		if adaptersRaw, ok := raw["adapters"].(map[string]interface{}); ok {
			delete(adaptersRaw, name)
		}
	})
}

func (c *Config) SetIMAdapterEnabled(name string, enabled bool) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	c.imAdaptersMu.Lock()
	defer c.imAdaptersMu.Unlock()
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
	return c.patchIMAdapterUnlocked(name, func(a map[string]interface{}) {
		a["enabled"] = enabled
	})
}

func (c *Config) SetIMAdapterExtra(name, key, value string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	c.imAdaptersMu.Lock()
	defer c.imAdaptersMu.Unlock()
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
	return c.patchIMAdapterUnlocked(name, func(a map[string]interface{}) {
		extra, _ := a["extra"].(map[string]interface{})
		if extra == nil {
			extra = map[string]interface{}{}
		}
		extra[key] = value
		a["extra"] = extra
	})
}

func (c *Config) SetIMAdapterEnv(name, key, value string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	c.imAdaptersMu.Lock()
	defer c.imAdaptersMu.Unlock()
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
	if adapter.Env == nil {
		adapter.Env = make(map[string]string)
	}
	adapter.Env[key] = value
	c.IM.Adapters[name] = adapter
	return c.patchIMAdapterUnlocked(name, func(a map[string]interface{}) {
		env, _ := a["env"].(map[string]interface{})
		if env == nil {
			env = map[string]interface{}{}
		}
		env[key] = value
		a["env"] = env
	})
}

func (c *Config) PatchIMAdapter(name string, patch func(adapter map[string]interface{})) error {
	c.imAdaptersMu.Lock()
	defer c.imAdaptersMu.Unlock()
	return c.patchIMAdapterUnlocked(name, patch)
}

func (c *Config) patchIMAdapterUnlocked(name string, patch func(adapter map[string]interface{})) error {

	if c.saveScope == "instance" {
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

	configDir := c.externalConfigDir()
	imPath := IMPath(configDir)

	unlock := lockConfigFile(imPath)
	defer unlock()

	raw := map[string]interface{}{}
	data, err := os.ReadFile(imPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading im config %s: %w", imPath, err)
	}
	if len(data) > 0 {

		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parsing im config %s: %w", imPath, err)
		}
	}

	adaptersRaw, _ := raw["adapters"].(map[string]interface{})
	if adaptersRaw == nil {
		adaptersRaw = map[string]interface{}{}
	}
	adapterRaw, _ := adaptersRaw[name].(map[string]interface{})
	if adapterRaw == nil {
		adapterRaw = map[string]interface{}{}
	}
	patch(adapterRaw)
	adaptersRaw[name] = adapterRaw
	raw["adapters"] = adaptersRaw

	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling im config: %w", err)
	}
	return writeSecureConfigFile(imPath, out)
}

func (c *Config) patchExternalFile(section string, patch func(raw map[string]interface{})) error {
	configDir := c.externalConfigDir()
	path := externalFilePath(configDir, section)

	unlock := lockConfigFile(path)
	defer unlock()

	raw := map[string]interface{}{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {

		return fmt.Errorf("reading %s: %w", section, err)
	}
	if len(data) > 0 {

		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parsing %s: %w", section, err)
		}
	}

	patch(raw)

	out, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", section, err)
	}
	return writeSecureConfigFile(path, out)
}

func (c *Config) externalConfigDir() string {

	if c.saveScope == "instance" && c.instanceDir != "" {
		return c.instanceDir
	}
	d := filepath.Dir(c.FilePath)
	if d == "." {
		return ConfigDir()
	}
	return d
}

func recompactConfigFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	data = stripDefaultsFromYAML(data)
	return writeSecureConfigFile(path, data)
}

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
