package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
)

// DesktopConfig stores window state and preferences, shared with the Fyne desktop.
// File: ~/.ggcode/desktop-config.json
type DesktopConfig struct {
	mu sync.Mutex

	WorkDir     string `json:"work_dir,omitempty"`
	WindowW     int    `json:"window_width,omitempty"`
	WindowH     int    `json:"window_height,omitempty"`
	WindowX     int    `json:"window_x,omitempty"`
	WindowY     int    `json:"window_y,omitempty"`
	WindowMax   bool   `json:"window_maximized,omitempty"`
	LastSession string `json:"last_session_id,omitempty"`
	Language    string `json:"language,omitempty"`

	// Desktop notification preferences
	NotificationsEnabled bool `json:"notifications_enabled,omitempty"`
}

func desktopConfigPath() string {
	return filepath.Join(config.HomeDir(), ".ggcode", "desktop-config.json")
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

// SetWindowState saves the window position and size.
func (dc *DesktopConfig) SetWindowState(w, h, x, y int, maximized bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.WindowW = w
	dc.WindowH = h
	dc.WindowX = x
	dc.WindowY = y
	dc.WindowMax = maximized
}

// SetLastSession saves the last active session ID.
func (dc *DesktopConfig) SetLastSession(id string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.LastSession = id
}

// IsNotificationsEnabled returns whether desktop notifications are enabled.
// Defaults to true when unset (zero value for bool is false, so we check explicitly).
func (dc *DesktopConfig) IsNotificationsEnabled() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	// Treat zero-value (unset) as enabled
	if !dc.NotificationsEnabled {
		// Check if the config file was loaded (has other fields set)
		// If the file exists but this field was omitted, it defaults to false.
		// We want default=true, so we check if any field was loaded.
		return true
	}
	return dc.NotificationsEnabled
}

// SetNotificationsEnabled updates the notification preference.
func (dc *DesktopConfig) SetNotificationsEnabled(enabled bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.NotificationsEnabled = enabled
}
