package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the main application struct for the Wails desktop app.
type App struct {
	ctx     context.Context
	workDir string
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.workDir, _ = os.Getwd()
}

// shutdown is called when the app is closing.
func (a *App) shutdown(_ context.Context) {}

// ─── Config ───────────────────────────────────────────────

// GetConfig returns the current config.
func (a *App) GetConfig() (*wailskit.ConfigSnapshot, error) {
	return wailskit.GetConfig()
}

// GetVendors returns available vendor names.
func (a *App) GetVendors() []string {
	return wailskit.VendorNames()
}

// GetEndpoints returns endpoints for the given vendor.
func (a *App) GetEndpoints(vendor string) []wailskit.EndpointInfo {
	return wailskit.EndpointsForVendor(vendor)
}

// GetModels returns models for the given vendor and endpoint.
func (a *App) GetModels(vendor, endpoint string) []string {
	return wailskit.ModelsForEndpoint(vendor, endpoint)
}

// SaveConfig saves config values from the frontend.
func (a *App) SaveConfig(values map[string]string) error {
	return wailskit.SaveConfig(values)
}

// ─── Workspace ────────────────────────────────────────────

// GetWorkDir returns the current working directory.
func (a *App) GetWorkDir() string {
	return a.workDir
}

// SelectDirectory opens a native directory picker.
func (a *App) SelectDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Project Directory",
	})
}

// ─── System ───────────────────────────────────────────────

// GetVersion returns the application version.
func (a *App) GetVersion() string {
	return "1.3.60"
}

// GetPlatform returns the current platform.
func (a *App) GetPlatform() string {
	return runtime.Environment(a.ctx).Platform
}

// ListFiles returns files in the given directory (1 level deep).
func (a *App) ListFiles(dir string) []map[string]interface{} {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var result []map[string]interface{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, map[string]interface{}{
			"name":     e.Name(),
			"isDir":    e.IsDir(),
			"size":     info.Size(),
			"modified": info.ModTime().Unix(),
		})
	}
	return result
}

// ReadFileContent reads a text file and returns its content.
func (a *App) ReadFileContent(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
