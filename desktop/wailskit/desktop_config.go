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

	// Font zoom level (0.7 to 1.8). Default 0 means 100%.
	FontZoom float64 `json:"font_zoom,omitempty"`

	// AlwaysOnTop keeps the window floating above other windows.
	AlwaysOnTop bool `json:"always_on_top,omitempty"`

	// GlobalHotkey enables a system-wide keyboard shortcut (Option+Cmd+G)
	// to toggle window visibility from any application.
	GlobalHotkey    bool `json:"global_hotkey,omitempty"`
	GlobalHotkeySet bool `json:"global_hotkey_configured,omitempty"`

	// Desktop notification preferences
	// NotificationsSet tracks whether the user has explicitly configured notifications.
	// When false (default), notifications are treated as enabled.
	NotificationsEnabled bool `json:"notifications_enabled,omitempty"`
	NotificationsSet     bool `json:"notifications_configured,omitempty"`
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
	if uerr := json.Unmarshal(data, dc); uerr != nil {
		// Corrupt/truncated config: return defaults but DO NOT let the
		// unconditional shutdown Save() overwrite the file — preserve the
		// original as .bak so the damage stays recoverable (#207).
		_ = os.Rename(desktopConfigPath(), desktopConfigPath()+".bak")
		return &DesktopConfig{WindowW: 1280, WindowH: 860}
	}
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
// Defaults to true when never explicitly set.
func (dc *DesktopConfig) IsNotificationsEnabled() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	// When not explicitly configured, default to enabled
	if !dc.NotificationsSet {
		return true
	}
	return dc.NotificationsEnabled
}

// SetNotificationsEnabled updates the notification preference and marks it as configured.
func (dc *DesktopConfig) SetNotificationsEnabled(enabled bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.NotificationsEnabled = enabled
	dc.NotificationsSet = true
}

// GetFontZoom returns the persisted font zoom level.
// Returns 1.0 (100%) when not set.
func (dc *DesktopConfig) GetFontZoom() float64 {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.FontZoom <= 0 {
		return 1.0
	}
	return dc.FontZoom
}

// SetFontZoom saves the font zoom level.
func (dc *DesktopConfig) SetFontZoom(zoom float64) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.FontZoom = zoom
}

// IsAlwaysOnTop returns whether the window should float above others.
func (dc *DesktopConfig) IsAlwaysOnTop() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.AlwaysOnTop
}

// SetAlwaysOnTop persists the always-on-top preference.
func (dc *DesktopConfig) SetAlwaysOnTop(on bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.AlwaysOnTop = on
}

// IsGlobalHotkeyEnabled returns whether the global hotkey is enabled.
// Defaults to true when never explicitly set (the hotkey enhances discoverability).
func (dc *DesktopConfig) IsGlobalHotkeyEnabled() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	// First-run default: enabled. Once the user explicitly toggles it,
	// GlobalHotkeySet becomes true and the explicit value takes effect.
	if !dc.GlobalHotkeySet {
		return true
	}
	return dc.GlobalHotkey
}

// SetGlobalHotkey persists the global hotkey preference.
func (dc *DesktopConfig) SetGlobalHotkey(on bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.GlobalHotkey = on
	dc.GlobalHotkeySet = true
}
