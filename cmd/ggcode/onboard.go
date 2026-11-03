package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/tui"
)

func runOnboardAndRestart(cfg *config.Config) error {
	result, err := tui.RunOnboard(cfg)
	if err != nil {
		return err
	}

	cfg.Language = result.Language

	if result.CustomProvider != nil {
		cp := result.CustomProvider

		vendorID := sanitizeVendorID(cp.Name)
		endpointID := "default"

		if err := cfg.AddVendor(vendorID, cp.Name, cp.APIKey); err != nil {

		}

		if err := cfg.AddEndpoint(vendorID, endpointID, cp.Protocol, cp.BaseURL, cp.APIKey); err != nil {
			return fmt.Errorf("creating custom endpoint: %w", err)
		}

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

		cfg.Vendor = result.VendorID
		cfg.Endpoint = result.EndpointID

		if _, ok := cfg.Vendors[result.VendorID]; !ok {
			dc := config.DefaultConfig()
			if vc, ok := dc.Vendors[result.VendorID]; ok {
				cfg.Vendors[result.VendorID] = vc
			}
		}

		if result.APIKey != "" {
			vc, ok := cfg.Vendors[result.VendorID]
			if ok {
				vc.APIKey = result.APIKey
				cfg.Vendors[result.VendorID] = vc
			}
		}

		cfg.Model = result.Model
		vc, ok := cfg.Vendors[result.VendorID]
		if ok {
			if ep, ok := vc.Endpoints[result.EndpointID]; ok {
				ep.SelectedModel = result.Model
				vc.Endpoints[result.EndpointID] = ep
				cfg.Vendors[result.VendorID] = vc
			}
		}
	}

	cfg.DefaultMode = result.Mode
	if result.Knight {
		cfg.KnightConfig = config.KnightConfig{Enabled: true}
	}
	if result.A2A {

		cfg.A2A.Disabled = false
	}

	if len(result.IMAdapters) > 0 {
		if cfg.IM.Adapters == nil {
			cfg.IM.Adapters = make(map[string]config.IMAdapterConfig)
		}
		for name, acfg := range result.IMAdapters {
			cfg.IM.Adapters[name] = acfg
		}
		cfg.IM.Enabled = true
	}

	if cfg.FilePath == "" {
		dir, _ := os.UserHomeDir()
		cfg.FilePath = filepath.Join(dir, ".ggcode", "ggcode.yaml")
	}

	cfg.FirstRun = false
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}
	args := os.Args[1:]

	if err := syscall.Exec(executable, append([]string{executable}, args...), os.Environ()); err != nil {
		if strings.Contains(err.Error(), "not supported") || runtime.GOOS == "windows" {
			fmt.Fprintln(os.Stderr, "Onboarding complete. Restart ggcode to continue (automatic restart is unavailable on this platform).")
			return nil
		}
		return fmt.Errorf("restarting after onboarding: %w", err)
	}
	return nil
}

func sanitizeVendorID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

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
