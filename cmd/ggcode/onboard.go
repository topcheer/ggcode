package main

import (
	"fmt"
	"os"
	"path/filepath"
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
