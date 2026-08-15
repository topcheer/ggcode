package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
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
		// Corrupt/truncated config (e.g. crash mid-write). Try the .bak copy
		// BEFORE resetting to defaults (#428): a partial write used to wipe
		// WorkDir/LastSession/Language/AlwaysOnTop/notification prefs, and
		// the shutdown Save() then persisted the defaults — silent total
		// loss of user preferences.
		if bak, berr := os.ReadFile(desktopConfigPath() + ".bak"); berr == nil {
			bdc := &DesktopConfig{WindowW: 1280, WindowH: 860}
			if json.Unmarshal(bak, bdc) == nil {
				debug.Log("wailskit", "desktop config corrupted; recovered from .bak")
				return bdc
			}
		}
		// No usable backup: return defaults but DO NOT let the unconditional
		// shutdown Save() overwrite the file — preserve the original as .bak
		// so the damage stays recoverable (#207). Rename errors are logged
		// (#428): overwriting a previous .bak silently destroyed the only
		// forensic sample of the earlier corruption.
		if rerr := os.Rename(desktopConfigPath(), desktopConfigPath()+".bak"); rerr != nil {
			debug.Log("wailskit", "failed to preserve corrupt desktop config as .bak: %v", rerr)
		}
		debug.Log("wailskit", "desktop config corrupted and no .bak available; using defaults")
		return &DesktopConfig{WindowW: 1280, WindowH: 860}
	}
	return dc
}

// Save persists the desktop config atomically and rotates a good copy to
// .bak (#449). The recovery path in LoadDesktopConfig reads .bak, but Save
// never produced one — a crash mid-write meant first corruption lost all
// preferences with no backup to recover from.
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
	// #449: rotate the current good copy (if any) to .bak BEFORE writing.
	if old, rerr := os.ReadFile(path); rerr == nil && len(old) > 0 {
		if werr := os.WriteFile(path+".bak", old, 0600); werr != nil {
			debug.Log("wailskit", "failed to rotate desktop config .bak: %v", werr)
		}
	}
	// Atomic write: temp file + rename, so a crash mid-write can never
	// leave a truncated main file.
	tmp := path + ".tmp"
	if werr := os.WriteFile(tmp, data, 0600); werr != nil {
		return werr
	}
	return os.Rename(tmp, path)
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
