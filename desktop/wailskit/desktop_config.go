package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

type DesktopConfig struct {
	mu sync.Mutex

	WorkDir   string `json:"work_dir,omitempty"`
	WindowW   int    `json:"window_width,omitempty"`
	WindowH   int    `json:"window_height,omitempty"`
	WindowX   int    `json:"window_x,omitempty"`
	WindowY   int    `json:"window_y,omitempty"`
	WindowMax bool   `json:"window_maximized,omitempty"`

	WindowPosSet bool   `json:"window_position_set,omitempty"`
	Language     string `json:"language,omitempty"`

	FontZoom float64 `json:"font_zoom,omitempty"`

	AlwaysOnTop bool `json:"always_on_top,omitempty"`

	GlobalHotkey    bool `json:"global_hotkey,omitempty"`
	GlobalHotkeySet bool `json:"global_hotkey_configured,omitempty"`

	NotificationsEnabled bool `json:"notifications_enabled,omitempty"`
	NotificationsSet     bool `json:"notifications_configured,omitempty"`

	// #5574: /et (etunnel) paid-plan settings. Desktop settings storage is
	// the single source of truth for the tunnel card — deliberately NOT the
	// webui config file (two storage planes stay separate).
	ETunnelSubdomain string `json:"etunnel_subdomain,omitempty"`
	ETunnelToken     string `json:"etunnel_token,omitempty"`
	ETunnelPlan      string `json:"etunnel_plan,omitempty"`

	dirtyWindowState   bool
	dirtyETunnel       bool
	dirtyFontZoom      bool
	dirtyAlwaysOnTop   bool
	dirtyGlobalHotkey  bool
	dirtyNotifications bool
	dirtyWorkDir       bool
}

func desktopConfigPath() string {
	return filepath.Join(config.HomeDir(), ".ggcode", "desktop-config.json")
}

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

func (dc *DesktopConfig) Save() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	path := desktopConfigPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

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
	dc.dirtyETunnel = false
	return nil
}

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
	if dc.dirtyETunnel {
		merged.ETunnelSubdomain = dc.ETunnelSubdomain
		merged.ETunnelToken = dc.ETunnelToken
		merged.ETunnelPlan = dc.ETunnelPlan
	}
}

// GetETunnel returns the persisted /et settings (subdomain, token, plan).
func (dc *DesktopConfig) GetETunnel() (subdomain, token, plan string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.ETunnelSubdomain, dc.ETunnelToken, dc.ETunnelPlan
}

// SetETunnel persists paid-plan credentials (marks dirty for Save merge).
func (dc *DesktopConfig) SetETunnel(subdomain, token string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.ETunnelSubdomain = subdomain
	dc.ETunnelToken = token
	dc.dirtyETunnel = true
}

func (dc *DesktopConfig) SetWorkDir(dir string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyWorkDir = true
	dc.WorkDir = dir
}

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

func (dc *DesktopConfig) IsNotificationsEnabled() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if !dc.NotificationsSet {
		return true
	}
	return dc.NotificationsEnabled
}

func (dc *DesktopConfig) SetNotificationsEnabled(enabled bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyNotifications = true
	dc.NotificationsEnabled = enabled
	dc.NotificationsSet = true
}

func (dc *DesktopConfig) GetFontZoom() float64 {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if dc.FontZoom <= 0 {
		return 1.0
	}
	return dc.FontZoom
}

func (dc *DesktopConfig) SetFontZoom(zoom float64) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyFontZoom = true
	dc.FontZoom = zoom
}

func (dc *DesktopConfig) IsAlwaysOnTop() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.AlwaysOnTop
}

func (dc *DesktopConfig) SetAlwaysOnTop(on bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyAlwaysOnTop = true
	dc.AlwaysOnTop = on
}

func (dc *DesktopConfig) IsGlobalHotkeyEnabled() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if !dc.GlobalHotkeySet {
		return true
	}
	return dc.GlobalHotkey
}

func (dc *DesktopConfig) SetGlobalHotkey(on bool) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.dirtyGlobalHotkey = true
	dc.GlobalHotkey = on
	dc.GlobalHotkeySet = true
}
