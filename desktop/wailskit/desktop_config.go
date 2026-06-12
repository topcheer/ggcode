package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// DesktopConfig stores window state and preferences, shared with the Fyne desktop.
// File: ~/.ggcode/desktop-config.json
type DesktopConfig struct {
	mu sync.Mutex

	WorkDir     string `json:"work_dir,omitempty"`
	WindowW     int    `json:"window_width,omitempty"`
	WindowH     int    `json:"window_height,omitempty"`
	LastSession string `json:"last_session_id,omitempty"`
	Language    string `json:"language,omitempty"`
}

func desktopConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ggcode", "desktop-config.json")
}

// LoadDesktopConfig reads the shared desktop config file.
func LoadDesktopConfig() *DesktopConfig {
	dc := &DesktopConfig{
		WindowW: 1280,
		WindowH: 860,
	}
	data, err := os.ReadFile(desktopConfigPath())
	if err != nil {
		return dc
	}
	_ = json.Unmarshal(data, dc)
	return dc
}

// Save persists the desktop config.
func (dc *DesktopConfig) Save() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
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

// SetWorkDir saves the work directory.
func (dc *DesktopConfig) SetWorkDir(dir string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.WorkDir = dir
}

// SetLastSession saves the last active session ID.
func (dc *DesktopConfig) SetLastSession(id string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.LastSession = id
}
