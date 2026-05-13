package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// DesktopConfig stores window position/size and preferences, independent
// of the main ggcode config (~/.ggcode/ggcode.yaml).
type DesktopConfig struct {
	mu sync.Mutex

	WorkDir     string `json:"work_dir,omitempty"`
	WindowW     int    `json:"window_width,omitempty"`
	WindowH     int    `json:"window_height,omitempty"`
	SidebarW    int    `json:"sidebar_width,omitempty"`
	Theme       string `json:"theme,omitempty"`
	Language    string `json:"language,omitempty"`
	LastSession string `json:"last_session_id,omitempty"`
}

func defaultDesktopConfig() *DesktopConfig {
	return &DesktopConfig{
		WindowW:  1200,
		WindowH:  800,
		SidebarW: 300,
		Theme:    "dark",
		Language: "en",
	}
}

func desktopConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ggcode", "desktop-config.json")
}

func LoadDesktopConfig() *DesktopConfig {
	dc := defaultDesktopConfig()
	data, err := os.ReadFile(desktopConfigPath())
	if err != nil {
		return dc
	}
	_ = json.Unmarshal(data, dc)
	return dc
}

func (dc *DesktopConfig) Save() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.saveLocked()
}

func (dc *DesktopConfig) saveLocked() error {
	path := desktopConfigPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(dc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
