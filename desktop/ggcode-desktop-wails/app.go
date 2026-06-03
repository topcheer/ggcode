package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the main application struct for the Wails desktop app.
type App struct {
	ctx              context.Context
	chat             *wailskit.ChatBridge
	workDir          string
	dc               *wailskit.DesktopConfig
	imManager        *im.Manager
	imController     *im.AdapterController
	imInstanceDetect *im.InstanceDetect
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

	// Initialize IM runtime (same as Fyne's initIMRuntime)
	a.initIMRuntime()
}

// shutdown is called when the app is closing.
func (a *App) shutdown(_ context.Context) {
	if a.chat != nil {
		a.chat.Cancel()
	}
	if a.imController != nil {
		a.imController.Stop()
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

// ─── IM Runtime (mirrors Fyne's initIMRuntime / im_bridge.go) ──────────

// wailsIMBridge implements im.Bridge, routing inbound IM messages to the Wails agent.
type wailsIMBridge struct {
	app *App
}

func (b *wailsIMBridge) SubmitInboundMessage(ctx context.Context, msg im.InboundMessage) error {
	if b.app == nil || b.app.chat == nil {
		return fmt.Errorf("app not available")
	}
	text := buildInboundText(msg)
	if text == "" {
		return nil
	}
	// Run in background — Wails doesn't need UI thread dispatch like Fyne
	safego.Run("im-inbound", func() {
		_ = b.app.chat.SendMessage(text)
	})
	return nil
}

func buildInboundText(msg im.InboundMessage) string {
	blocks := msg.ProviderContent()
	if len(blocks) == 0 {
		return strings.TrimSpace(msg.Text)
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return strings.TrimSpace(msg.Text)
}

// initIMRuntime initializes the IM manager once at app startup.
// Direct port of Fyne's App.initIMRuntime().
func (a *App) initIMRuntime() {
	if a.imManager != nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("initIMRuntime panic: %v\n", r)
		}
	}()

	mgr := im.NewManager()

	bindingsPath, err := im.DefaultBindingsPath()
	if err != nil {
		return
	}
	bindingStore, err := im.NewJSONFileBindingStore(bindingsPath)
	if err != nil {
		return
	}
	if err := mgr.SetBindingStore(bindingStore); err != nil {
		return
	}

	pairingPath, err := im.DefaultPairingStatePath()
	if err != nil {
		return
	}
	pairingStore, err := im.NewJSONFilePairingStore(pairingPath)
	if err != nil {
		return
	}
	if err := mgr.SetPairingStore(pairingStore); err != nil {
		return
	}

	workDir := ""
	if a.dc != nil {
		workDir = a.dc.WorkDir
	}
	mgr.BindSession(im.SessionBinding{Workspace: workDir})

	cfg, _ := wailskit.LoadConfigForWorkspace(workDir)
	if cfg != nil && cfg.IM.Adapters != nil {
		adapters := make(map[string]bool)
		for name, acfg := range cfg.IM.Adapters {
			adapters[name] = acfg.Enabled
		}
		mgr.ApplyAdapterConfig(adapters)
	}

	// Pairing UI via frontend event
	mgr.SetOnUpdate(func(snap im.StatusSnapshot) {
		if snap.PendingPairing != nil {
			ch := snap.PendingPairing
			runtime.EventsEmit(a.ctx, "im:pairing", map[string]string{
				"adapter": ch.Adapter, "platform": string(ch.Platform), "code": ch.Code,
			})
		}
	})

	a.imManager = mgr

	// Multi-instance detection — auto-mute if another instance is primary
	if workDir != "" {
		detect, others, err := mgr.RegisterInstance(workDir)
		if err == nil && detect != nil {
			a.imInstanceDetect = detect
			if len(others) > 0 {
				count, _ := mgr.MuteAll()
				if count > 0 {
					fmt.Printf("im: auto-muted %d channel(s), another instance is primary\n", count)
				}
			}
		}
	}

	// Start adapters bound to current workspace
	a.startIMAdapters()

	// Bind IM emitter to chat bridge for outbound push
	if a.chat != nil {
		lang := ""
		if cfg != nil {
			lang = cfg.Language
		}
		a.chat.Emitter = im.NewIMEmitter(mgr, lang, workDir)
	}
}

// startIMAdapters starts all enabled adapters bound to the current workspace.
func (a *App) startIMAdapters() {
	if a.imManager == nil {
		return
	}
	cfg, _ := wailskit.LoadConfigForWorkspace(a.workDir)
	if cfg == nil || !cfg.IM.Enabled {
		return
	}

	a.imManager.SetBridge(&wailsIMBridge{app: a})

	controller, err := im.StartCurrentBindingAdapter(context.Background(), cfg.IM, a.imManager)
	if err != nil {
		fmt.Printf("IM adapter start error: %v\n", err)
		return
	}
	a.imController = controller
}

// stopIMAdapters stops all running IM adapters.
func (a *App) stopIMAdapters() {
	if a.imController != nil {
		a.imController.Stop()
		a.imController = nil
	}
	if a.imInstanceDetect != nil {
		a.imInstanceDetect.Unregister()
		a.imInstanceDetect = nil
	}
}

// ─── IM Frontend API ──────────────────────────────────────────────────

// ListIMAdapters returns all configured IM adapters with binding info.
func (a *App) ListIMAdapters() ([]wailskit.IMAdapterInfo, error) {
	return wailskit.ListIMAdapters(a.workDir, a.imManager)
}

// GetIMPlatformRegistry returns supported IM platforms.
func (a *App) GetIMPlatformRegistry() []wailskit.IMPlatformMeta {
	return wailskit.GetIMPlatformRegistry()
}

// SaveIMAdapter creates or updates an IM adapter.
func (a *App) SaveIMAdapter(name string, values map[string]string) error {
	return wailskit.SaveIMAdapter(name, values)
}

// RemoveIMAdapter removes an IM adapter by name.
func (a *App) RemoveIMAdapter(name string) error {
	return wailskit.RemoveIMAdapter(name)
}

// SetIMAdapterEnabled enables or disables an IM adapter.
func (a *App) SetIMAdapterEnabled(name string, enabled bool) error {
	return wailskit.SetIMAdapterEnabled(name, enabled)
}

// MuteIMAdapter mutes or unmutes an adapter channel.
func (a *App) MuteIMAdapter(name string, muted bool) error {
	if a.imManager == nil {
		return fmt.Errorf("IM not initialized")
	}
	if muted {
		return a.imManager.MuteBinding(name)
	}
	return a.imManager.UnmuteBinding(name)
}
