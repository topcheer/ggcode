package wailskit

import (
	"fmt"
	"sort"

	"github.com/topcheer/ggcode/internal/config"
)

// IMAdapterInfo is a frontend-friendly representation of an IM adapter config.
type IMAdapterInfo struct {
	Name      string                 `json:"name"`
	Enabled   bool                   `json:"enabled"`
	Platform  string                 `json:"platform"`
	Transport string                 `json:"transport"`
	Command   string                 `json:"command"`
	Args      []string               `json:"args,omitempty"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

// ListIMAdapters returns all configured IM adapters.
func ListIMAdapters() ([]IMAdapterInfo, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.IM.Adapters == nil {
		return nil, nil
	}

	var result []IMAdapterInfo
	for name, acfg := range cfg.IM.Adapters {
		result = append(result, IMAdapterInfo{
			Name:      name,
			Enabled:   acfg.Enabled,
			Platform:  acfg.Platform,
			Transport: acfg.Transport,
			Command:   acfg.Command,
			Args:      acfg.Args,
			Extra:     acfg.Extra,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// SaveIMAdapter creates or updates an IM adapter. The cfg map may contain:
//   - "platform" (required): adapter platform (e.g. "telegram", "discord", "slack")
//   - "transport": transport type
//   - "command": command for stdio transport
//   - other keys are stored in the adapter's Extra map
func SaveIMAdapter(name string, values map[string]string) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	platform := values["platform"]
	if platform == "" {
		return fmt.Errorf("platform is required")
	}

	adapterCfg := config.IMAdapterConfig{
		Enabled:   true,
		Platform:  platform,
		Transport: values["transport"],
		Command:   values["command"],
	}

	// Collect remaining keys as Extra
	extra := make(map[string]interface{})
	for k, v := range values {
		switch k {
		case "platform", "transport", "command", "enabled":
			// handled separately
		default:
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		adapterCfg.Extra = extra
	}

	// Check if updating an existing adapter
	if _, exists := cfg.IM.Adapters[name]; exists {
		// Preserve existing Extra fields not in the update, then overwrite
		if cfg.IM.Adapters[name].Extra != nil {
			if adapterCfg.Extra == nil {
				adapterCfg.Extra = make(map[string]interface{})
			}
			for k, v := range cfg.IM.Adapters[name].Extra {
				if _, inUpdate := extra[k]; !inUpdate {
					adapterCfg.Extra[k] = v
				}
			}
		}
		// Remove old adapter and re-add (AddIMAdapter rejects existing names)
		delete(cfg.IM.Adapters, name)
		_ = cfg.Save()
	}

	return cfg.AddIMAdapter(name, adapterCfg)
}

// RemoveIMAdapter removes an IM adapter by name.
func RemoveIMAdapter(name string) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return cfg.RemoveIMAdapter(name)
}

// SetIMAdapterEnabled toggles the enabled state of an IM adapter.
func SetIMAdapterEnabled(name string, enabled bool) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return cfg.SetIMAdapterEnabled(name, enabled)
}

// TestIMConnection attempts to validate an IM adapter configuration.
// It performs a basic connectivity check by verifying the config has
// the minimum required fields for the given platform.
func TestIMConnection(name string) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.IM.Adapters == nil {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	acfg, ok := cfg.IM.Adapters[name]
	if !ok {
		return fmt.Errorf("IM adapter %q not found", name)
	}
	if acfg.Platform == "" {
		return fmt.Errorf("adapter %q has no platform configured", name)
	}
	// Basic validation — full adapter creation requires the im.Manager runtime.
	// The frontend should start the adapter and observe state changes.
	return nil
}
