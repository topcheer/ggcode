package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// DesktopConfig stores window state and preferences.
// File: ~/.ggcode/desktop-config.json
type DesktopConfig struct {
	mu sync.Mutex

	WorkDir   string `json:"work_dir,omitempty"`
	WindowW   int    `json:"window_width,omitempty"`
	WindowH   int    `json:"window_height,omitempty"`
	WindowX   int    `json:"window_x,omitempty"`
	WindowY   int    `json:"window_y,omitempty"`
	WindowMax bool   `json:"window_maximized,omitempty"`
	// WindowPosSet records that a normal-mode window position (possibly (0,0))
	// was captured, disambiguating a legitimate origin from the zero-value
	// "unset" state (#631).
	WindowPosSet bool   `json:"window_position_set,omitempty"`
	Language     string `json:"language,omitempty"`

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

	// #635: per-field dirty flags. dc is a full in-memory snapshot, not a
	// dirty-field tracker, and bools have no "non-default" value to guard on
	// (the #583 Bug 3 string/int guards can't work for them). Two desktop
	// instances share the config file: instance B toggles notifications and
	// saves; instance A (stale snapshot) then saves on window move or the
	// unconditional shutdown Save — A's stale bools silently rolled back B's
	// change. Only fields explicitly touched via a setter since load are
	// written into the merged on-disk state; unexported so they never
	// serialize.
	dirtyWindowState   bool // SetWindowState ran (WindowMax/WindowPosSet/bounds)
	dirtyFontZoom      bool // SetFontZoom ran
	dirtyAlwaysOnTop   bool // SetAlwaysOnTop ran
	dirtyGlobalHotkey  bool // SetGlobalHotkey ran
	dirtyNotifications bool // SetNotificationsEnabled ran
	dirtyWorkDir       bool // #710: SetWorkDir ran since the last successful Save
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

		if bak, berr := os.ReadFile(desktopConfigPath() + ".bak"); berr == nil {
			bdc := &DesktopConfig{WindowW: 1280, WindowH: 860}
			if json.Unmarshal(bak, bdc) == nil {

				if werr := os.WriteFile(desktopConfigPath(), bak, 0600); werr != nil {
					debug.Log("wailskit", "failed to promote .bak to main config: %v", werr)

				} else {
					debug.Log("wailskit", "desktop config corrupted; recovered and promoted from .bak")
				}
				return bdc
			}
		}

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
// #583 (Bug 3): Performs read-merge before writing to prevent multi-instance
// clobbering. An instance with an old snapshot would otherwise overwrite newer
// preferences set by another instance. The merge strategy: read disk, then
// apply only non-empty/non-default fields from the current instance, preserving
// other instances' changes.
func (dc *DesktopConfig) Save() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	path := desktopConfigPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// #583 (Bug 3): Read-merge to preserve other instances' changes.
	// Load current disk state and merge with this instance's dirty fields.
	var merged DesktopConfig
	if data, rerr := os.ReadFile(path); rerr == nil && len(data) > 0 {

		if uerr := json.Unmarshal(data, &merged); uerr != nil {

			debug.Log("wailskit", "Save: disk config is corrupt, using defaults for merge: %v", uerr)
		}
	}

	merged.mergeFromDirty(dc)

	data, err := json.MarshalIndent(&merged, "", "  ")
	if err != nil {
		return err
	}

	if old, rerr := os.ReadFile(path); rerr == nil && len(old) > 0 {
		testCfg := &DesktopConfig{}
		if json.Unmarshal(old, testCfg) == nil {
			if werr := os.WriteFile(path+".bak", old, 0600); werr != nil {
				debug.Log("wailskit", "failed to rotate desktop config .bak: %v", werr)
			}
		} else {
			debug.Log("wailskit", "skipping desktop config .bak rotation: existing file is unparseable")
		}
	}

	tmp := path + ".tmp"
	if werr := os.WriteFile(tmp, data, 0600); werr != nil {
		return werr
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}

	dc.dirtyWorkDir = false
	return nil
}

// mergeFromDirty overlays the disk-state receiver with the fields dc actually
// touched (#583 string/int non-empty guards; #635 dirty flags for bools and
// FontZoom, which have no "non-default" sentinel). Untouched fields keep the
// on-disk values so a stale snapshot from another instance cannot roll back
// preferences that instance saved meanwhile. Pointer receiver on purpose:
// DesktopConfig embeds a sync.Mutex, so a value receiver would copy the lock
// (vet copylocks). Called from Save with dc.mu held; touches plain fields only.
func (merged *DesktopConfig) mergeFromDirty(dc *DesktopConfig) {

	if dc.dirtyWorkDir {
		merged.WorkDir = dc.WorkDir
	}

	if dc.Language != "" {
		merged.Language = dc.Language
	}

	if dc.dirtyWindowState {
		merged.WindowW = dc.WindowW
		merged.WindowH = dc.WindowH
		merged.WindowX = dc.WindowX
		merged.WindowY = dc.WindowY
		merged.WindowMax = dc.WindowMax
		merged.WindowPosSet = dc.WindowPosSet
	}

	if dc.Language != "" {
		merged.Language = dc.Language
	}

	if dc.dirtyFontZoom {
		merged.FontZoom = dc.FontZoom
	}
	if dc.dirtyAlwaysOnTop {
		merged.AlwaysOnTop = dc.AlwaysOnTop
	}
	if dc.dirtyGlobalHotkey {
		merged.GlobalHotkey = dc.GlobalHotkey
		merged.GlobalHotkeySet = dc.GlobalHotkeySet
	}
	if dc.dirtyNotifications {
		merged.NotificationsEnabled = dc.NotificationsEnabled
		merged.NotificationsSet = dc.NotificationsSet
	}
}

// SetWorkDir saves the work directory. #710: marks the field dirty so only
// an instance that actually switched workspace writes it into the merged
// on-disk state (same #635 pattern as the toggles).
func (dc *DesktopConfig) SetWorkDir(dir string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyWorkDir = true
	dc.WorkDir = dir
}

// SetWindowState saves the window position and size.
// #631: while maximized the OS reports fullscreen bounds; persisting them
// would destroy the remembered normal bounds and a later unmaximize leaves
// a fullscreen-sized "normal" window. Only the flag is updated in that
// case, preserving the last captured normal bounds (and position flag).
func (dc *DesktopConfig) SetWindowState(w, h, x, y int, maximized bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyWindowState = true
	if maximized {
		dc.WindowMax = true
		return
	}
	dc.WindowW = w
	dc.WindowH = h
	dc.WindowX = x
	dc.WindowY = y

	dc.WindowPosSet = true
	dc.WindowMax = false
}

// IsNotificationsEnabled returns whether desktop notifications are enabled.
// Defaults to true when never explicitly set.
func (dc *DesktopConfig) IsNotificationsEnabled() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if !dc.NotificationsSet {
		return true
	}
	return dc.NotificationsEnabled
}

// SetNotificationsEnabled updates the notification preference and marks it as configured.
func (dc *DesktopConfig) SetNotificationsEnabled(enabled bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyNotifications = true
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
	dc.dirtyFontZoom = true
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
	dc.dirtyAlwaysOnTop = true
	dc.AlwaysOnTop = on
}

// IsGlobalHotkeyEnabled returns whether the global hotkey is enabled.
// Defaults to true when never explicitly set (the hotkey enhances discoverability).
func (dc *DesktopConfig) IsGlobalHotkeyEnabled() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if !dc.GlobalHotkeySet {
		return true
	}
	return dc.GlobalHotkey
}

// SetGlobalHotkey persists the global hotkey preference.
func (dc *DesktopConfig) SetGlobalHotkey(on bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyGlobalHotkey = true
	dc.GlobalHotkey = on
	dc.GlobalHotkeySet = true
}
