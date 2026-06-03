package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the main application struct for the Wails desktop app.
type App struct {
	ctx     context.Context
	chat    *wailskit.ChatBridge
	workDir string
	dc      *wailskit.DesktopConfig
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Load shared desktop config (same file as Fyne desktop)
	a.dc = wailskit.LoadDesktopConfig()

	// Restore last workspace
	if a.dc.WorkDir != "" {
		a.workDir = a.dc.WorkDir
		_ = os.Chdir(a.workDir)
	} else {
		a.workDir, _ = os.Getwd()
	}

	a.initWorkspace(a.workDir)
}

func (a *App) initWorkspace(dir string) {
	cfg, err := wailskit.LoadConfigForWorkspace(dir)
	if err != nil {
		cfg = nil
	}
	wailskit.SetConfig(cfg)

	// Save workdir to shared desktop config (mirrors Fyne dc.Save)
	a.dc.SetWorkDir(dir)
	_ = a.dc.Save()

	// Initialize chat bridge with loaded config
	chat, err := wailskit.NewChatBridge()
	if err != nil {
		return
	}
	chat.OnStreamEvent = func(eventType string, data json.RawMessage) {
		runtime.EventsEmit(a.ctx, "chat:stream", map[string]interface{}{
			"type": eventType,
			"data": string(data),
		})
	}
	a.chat = chat
	wailskit.SetChatBridge(chat)
}

// shutdown is called when the app is closing.
func (a *App) shutdown(_ context.Context) {
	if a.chat != nil {
		a.chat.Cancel()
	}
}

// ─── Workspace Init ──────────────────────────────────────

// NeedsOnboard returns true if the config needs first-time setup.
func (a *App) NeedsOnboard() bool {
	return wailskit.NeedsOnboard()
}

// SelectWorkspace opens a native directory picker and initializes the workspace.
func (a *App) SelectWorkspace() (string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Project Directory",
	})
	if err != nil || dir == "" {
		return "", err
	}
	_ = os.Chdir(dir)
	a.workDir = dir
	a.initWorkspace(dir)
	return dir, nil
}

// InitWorkspace initializes the workspace at the given directory.
func (a *App) InitWorkspace(dir string) error {
	_ = os.Chdir(dir)
	a.workDir = dir
	a.initWorkspace(dir)
	return nil
}

// CompleteOnboard saves vendor/endpoint/model/apiKey and finishes onboarding.
func (a *App) CompleteOnboard(vendor, endpoint, model, apiKey string) error {
	if err := wailskit.UpdateConfig(map[string]interface{}{
		"vendor":   vendor,
		"endpoint": endpoint,
		"model":    model,
	}); err != nil {
		return err
	}
	if apiKey != "" {
		if err := wailskit.SaveAPIKey(vendor, endpoint, apiKey); err != nil {
			return err
		}
	}
	// Reload chat bridge with new config
	a.initWorkspace(a.workDir)
	return nil
}

// GetVendorPresets returns vendor presets for onboarding.
func (a *App) GetVendorPresets() []wailskit.VendorPresetInfo {
	return wailskit.GetVendorPresets()
}

// ─── Chat ─────────────────────────────────────────────────

// SendMessage sends a user message to the agent.
func (a *App) SendMessage(userMsg string) error {
	if a.chat == nil {
		return nil
	}
	return a.chat.SendMessage(userMsg)
}

// CancelMessage cancels the current agent run.
func (a *App) CancelMessage() {
	if a.chat != nil {
		a.chat.Cancel()
	}
}

// GetModelInfo returns current model info for the status bar.
func (a *App) GetModelInfo() map[string]interface{} {
	if a.chat == nil {
		return nil
	}
	return a.chat.GetModelInfo()
}

// ─── Config ───────────────────────────────────────────────

// GetConfig returns the current config.
func (a *App) GetConfig() (*wailskit.FullConfig, error) {
	return wailskit.GetFullConfig()
}

// UpdateConfig applies config values and saves.
func (a *App) UpdateConfig(values map[string]interface{}) error {
	return wailskit.UpdateConfig(values)
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

// SaveAPIKey saves an API key for a vendor/endpoint.
func (a *App) SaveAPIKey(vendor, endpoint, apiKey string) error {
	return wailskit.SaveAPIKey(vendor, endpoint, apiKey)
}

// GetImpersonationPresets returns real presets from provider.
func (a *App) GetImpersonationPresets() []wailskit.ImpersonationPresetInfo {
	return wailskit.GetImpersonationPresets()
}

// ApplyImpersonation applies an impersonation preset.
func (a *App) ApplyImpersonation(presetID, version string, customHeaders map[string]string) error {
	return wailskit.ApplyImpersonation(presetID, version, customHeaders)
}

// TestEndpointConnection tests an endpoint by listing models.
func (a *App) TestEndpointConnection(protocol, baseURL, apiKey string) (*wailskit.TestEndpointResult, error) {
	return wailskit.TestEndpointConnection(protocol, baseURL, apiKey)
}

// AddCustomEndpoint adds a new custom endpoint to a vendor.
func (a *App) AddCustomEndpoint(vendor, name, protocol, baseURL, apiKey string) error {
	return wailskit.AddCustomEndpoint(vendor, name, protocol, baseURL, apiKey)
}

// ─── Sessions ─────────────────────────────────────────────

// ListSessions returns sessions for the current workspace.
func (a *App) ListSessions() ([]wailskit.SessionInfo, error) {
	return wailskit.ListSessions(a.workDir)
}

// DeleteSession removes a session by ID.
func (a *App) DeleteSession(id string) error {
	return wailskit.DeleteSession(id)
}

// NewSession creates a fresh session, cancelling any current work.
func (a *App) NewSession() error {
	if a.chat != nil {
		a.chat.Cancel()
	}
	return wailskit.NewSession()
}

// LoadSession loads an existing session by ID.
func (a *App) LoadSession(id string) error {
	if a.chat != nil {
		a.chat.Cancel()
	}
	return wailskit.LoadSession(id)
}

// GetSessionHistory returns messages from the current session.
func (a *App) GetSessionHistory() ([]wailskit.SessionMessage, error) {
	return wailskit.GetSessionHistory()
}

// ─── Workspace ────────────────────────────────────────────

// GetWorkDir returns the current working directory.
func (a *App) GetWorkDir() string {
	return a.workDir
}

// SaveDefaultMode saves the default permission mode.
func (a *App) SaveDefaultMode(mode string) error {
	return wailskit.SaveDefaultMode(mode)
}

// SelectDirectory opens a native directory picker.
func (a *App) SelectDirectory() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Directory",
	})
}

// ─── IM Adapters ─────────────────────────────────────────

// ListIMAdapters returns all configured IM adapters.
func (a *App) ListIMAdapters() ([]wailskit.IMAdapterInfo, error) {
	return wailskit.ListIMAdapters()
}

// SaveIMAdapter saves an IM adapter configuration.
func (a *App) SaveIMAdapter(name string, values map[string]string) error {
	return wailskit.SaveIMAdapter(name, values)
}

// RemoveIMAdapter removes an IM adapter.
func (a *App) RemoveIMAdapter(name string) error {
	return wailskit.RemoveIMAdapter(name)
}

// SetIMAdapterEnabled enables or disables an IM adapter.
func (a *App) SetIMAdapterEnabled(name string, enabled bool) error {
	return wailskit.SetIMAdapterEnabled(name, enabled)
}

// TestIMConnection tests the connection for an IM adapter.
func (a *App) TestIMConnection(name string) error {
	return wailskit.TestIMConnection(name)
}

// ─── MCP Servers ─────────────────────────────────────────

// ListMCPServers returns all configured MCP servers.
func (a *App) ListMCPServers() ([]wailskit.MCPServerInfo, error) {
	return wailskit.ListMCPServers()
}

// AddMCPServer adds a new MCP server.
func (a *App) AddMCPServer(values map[string]string) error {
	return wailskit.AddMCPServer(values)
}

// RemoveMCPServer removes an MCP server.
func (a *App) RemoveMCPServer(name string) error {
	return wailskit.RemoveMCPServer(name)
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
