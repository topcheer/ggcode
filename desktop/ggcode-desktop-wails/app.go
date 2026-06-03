package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the main application struct for the Wails desktop app.
type App struct {
	ctx     context.Context
	chat    *wailskit.ChatBridge
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

	// Initialize chat bridge
	chat, err := wailskit.NewChatBridge()
	if err != nil {
		println("Warning: chat bridge init error:", err.Error())
	} else {
		// Wire up streaming events to frontend
		chat.OnStreamEvent = func(eventType string, data json.RawMessage) {
			runtime.EventsEmit(a.ctx, "chat:stream", map[string]interface{}{
				"type": eventType,
				"data": string(data),
			})
		}
		a.chat = chat
	}
}

// shutdown is called when the app is closing.
func (a *App) shutdown(_ context.Context) {
	if a.chat != nil {
		a.chat.Cancel()
	}
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

// ─── Sessions ─────────────────────────────────────────────

// ListSessions returns all sessions sorted by UpdatedAt desc.
func (a *App) ListSessions() ([]wailskit.SessionInfo, error) {
	return wailskit.ListSessions()
}

// DeleteSession removes a session by ID.
func (a *App) DeleteSession(id string) error {
	return wailskit.DeleteSession(id)
}

// ─── IM Adapters ──────────────────────────────────────────

// ListIMAdapters returns all configured IM adapters.
func (a *App) ListIMAdapters() ([]wailskit.IMAdapterInfo, error) {
	return wailskit.ListIMAdapters()
}

// SaveIMAdapter creates or updates an IM adapter configuration.
func (a *App) SaveIMAdapter(name string, cfg map[string]string) error {
	return wailskit.SaveIMAdapter(name, cfg)
}

// RemoveIMAdapter removes an IM adapter by name.
func (a *App) RemoveIMAdapter(name string) error {
	return wailskit.RemoveIMAdapter(name)
}

// SetIMAdapterEnabled toggles the enabled state of an IM adapter.
func (a *App) SetIMAdapterEnabled(name string, enabled bool) error {
	return wailskit.SetIMAdapterEnabled(name, enabled)
}

// TestIMConnection validates an IM adapter configuration.
func (a *App) TestIMConnection(name string) error {
	return wailskit.TestIMConnection(name)
}

// ─── MCP Servers ──────────────────────────────────────────

// ListMCPServers returns all configured MCP servers.
func (a *App) ListMCPServers() ([]wailskit.MCPServerInfo, error) {
	return wailskit.ListMCPServers()
}

// AddMCPServer adds a new MCP server configuration.
func (a *App) AddMCPServer(cfg map[string]string) error {
	return wailskit.AddMCPServer(cfg)
}

// RemoveMCPServer removes an MCP server by name.
func (a *App) RemoveMCPServer(name string) error {
	return wailskit.RemoveMCPServer(name)
}

// ─── Files ────────────────────────────────────────────────

// ListDirectory returns file entries in the given directory.
// If recursive is true, it walks subdirectories.
func (a *App) ListDirectory(dir string, recursive bool) ([]wailskit.FileInfo, error) {
	return wailskit.ListDirectory(dir, recursive)
}

// ReadFileContent reads a text file and returns its content.
func (a *App) ReadFileContent(path string) (string, error) {
	return wailskit.ReadFileContent(path)
}

// GetWorkingDir returns the current working directory.
func (a *App) GetWorkingDir() string {
	return a.workDir
}

// ─── Workspace ────────────────────────────────────────────

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
// Deprecated: Use ListDirectory instead for richer file info.
func (a *App) ListFiles(dir string) []map[string]interface{} {
	entries, err := wailskit.ListDirectory(dir, false)
	if err != nil {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		result = append(result, map[string]interface{}{
			"name":     e.Name,
			"isDir":    e.IsDir,
			"size":     e.Size,
			"modified": e.Modified,
		})
	}
	return result
}
