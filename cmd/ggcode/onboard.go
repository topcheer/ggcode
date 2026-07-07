package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tui"
)

// runOnboardAndRestart launches the onboard wizard and restarts ggcode on success.
func runOnboardAndRestart(cfg *config.Config) error {
	result, err := tui.RunOnboard(cfg)
	if err != nil {
		return err
	}

	// Apply language.
	cfg.Language = result.Language

	// Handle custom provider — create vendor + endpoint dynamically.
	if result.CustomProvider != nil {
		cp := result.CustomProvider
		// Sanitize vendor ID from display name.
		vendorID := sanitizeVendorID(cp.Name)
		endpointID := "default"

		// Create vendor if not exists.
		if err := cfg.AddVendor(vendorID, cp.Name, cp.APIKey); err != nil {
			// Vendor may already exist from a previous onboard; that's fine.
		}
		// Create endpoint.
		if err := cfg.AddEndpoint(vendorID, endpointID, cp.Protocol, cp.BaseURL, cp.APIKey); err != nil {
			return fmt.Errorf("creating custom endpoint: %w", err)
		}
		// Set default model on the endpoint.
		if vc, ok := cfg.Vendors[vendorID]; ok {
			if ep, ok := vc.Endpoints[endpointID]; ok {
				ep.DefaultModel = cp.Model
				ep.SelectedModel = cp.Model
				vc.Endpoints[endpointID] = ep
				cfg.Vendors[vendorID] = vc
			}
		}

		cfg.Vendor = vendorID
		cfg.Endpoint = endpointID
		cfg.Model = cp.Model
	} else {
		// Apply vendor and endpoint selection.
		cfg.Vendor = result.VendorID
		cfg.Endpoint = result.EndpointID

		// Apply API key: set at vendor level.
		if result.APIKey != "" {
			vc, ok := cfg.Vendors[result.VendorID]
			if ok {
				vc.APIKey = result.APIKey
				cfg.Vendors[result.VendorID] = vc
			}
		}

		// Apply model.
		cfg.Model = result.Model
		ep, ok := cfg.Vendors[result.VendorID].Endpoints[result.EndpointID]
		if ok {
			ep.SelectedModel = result.Model
			cfg.Vendors[result.VendorID].Endpoints[result.EndpointID] = ep
		}
	}

	// Apply optional settings — only set when user explicitly opted in.
	cfg.DefaultMode = result.Mode
	if result.Knight {
		cfg.KnightConfig = config.KnightConfig{Enabled: true}
	}
	if result.A2A {
		cfg.A2A = config.A2AConfig{Disabled: false}
	}

	// Apply IM adapters from onboard.
	if len(result.IMAdapters) > 0 {
		if cfg.IM.Adapters == nil {
			cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
		}
		for name, acfg := range result.IMAdapters {
			cfg.IM.Adapters[name] = acfg
		}
		cfg.IM.Enabled = true
	}

	// Ensure config file path is set.
	if cfg.FilePath == "" {
		dir, _ := os.UserHomeDir()
		cfg.FilePath = filepath.Join(dir, ".ggcode", "ggcode.yaml")
	}

	cfg.FirstRun = false
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	// Restart ggcode.
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}
	args := os.Args[1:]
	return syscall.Exec(executable, append([]string{executable}, args...), os.Environ())
}

// sanitizeVendorID creates a YAML-safe vendor ID from a display name.
func sanitizeVendorID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	// Remove any char that's not lowercase letter, digit, or hyphen.
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if result == "" {
		result = "custom"
	}
	return result
}
