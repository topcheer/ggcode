package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/update"
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
	// Mobile tunnel
	tunnelMu      sync.RWMutex
	tunnelSession *tunnel.Session
	tunnelBroker  *tunnel.Broker

	// Current ask_user request (for mobile response mapping)
	askUserMu     sync.Mutex
	askUserReq    tool.AskUserRequest
	hasAskUserReq bool

	streamEvents chan uiEvent
	streamOnce   sync.Once
	streamMu     sync.Mutex
	streamQueue  []StreamEventEnvelope
}

type uiEvent struct {
	name    string
	payload interface{}
}

type StreamEventEnvelope struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startEventLoop()

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

func (a *App) startEventLoop() {
	a.streamOnce.Do(func() {
		a.streamEvents = make(chan uiEvent, 4096)
		safego.Go("wails-event-loop", func() {
			for ev := range a.streamEvents {
				if a.ctx == nil {
					continue
				}
				runtime.EventsEmit(a.ctx, ev.name, ev.payload)
			}
		})
	})
}

func (a *App) enqueueUIEvent(name string, payload interface{}) {
	if a.streamEvents == nil {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, name, payload)
		}
		return
	}
	a.streamEvents <- uiEvent{name: name, payload: payload}
}

func (a *App) initWorkspace(dir string) {
	if dir == "" {
		return
	}
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
		a.emitStreamEvent(eventType, data)
	}
	chat.EmitEvent = func(name string, payload ...interface{}) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, name, payload...)
		}
	}
	a.chat = chat
	wailskit.SetChatBridge(chat)

	// Initialize IM runtime (same as Fyne's initIMRuntime)
	a.initIMRuntime()

	// Auto-create initial session and start agent so MCP servers
	// and context window are available immediately on startup
	chat.EnsureSession()
	_ = chat.InitAgent()

	// Start IM adapters AFTER InitAgent so the bridge has the correct chat instance
	a.startIMAdapters()

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "workspace:changed", map[string]interface{}{
			"workDir": dir,
		})
		runtime.EventsEmit(a.ctx, "config:updated", nil)
	}
}

func (a *App) DrainStreamEvents() []StreamEventEnvelope {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if len(a.streamQueue) == 0 {
		return nil
	}
	out := make([]StreamEventEnvelope, len(a.streamQueue))
	copy(out, a.streamQueue)
	a.streamQueue = a.streamQueue[:0]
	return out
}

func (a *App) emitStreamEvent(eventType string, data json.RawMessage) {
	if a.ctx == nil {
		return
	}
	envelope := StreamEventEnvelope{
		Type: eventType,
		Data: string(data),
	}
	a.streamMu.Lock()
	a.streamQueue = append(a.streamQueue, envelope)
	a.streamMu.Unlock()
	a.enqueueUIEvent("chat:stream", map[string]interface{}{
		"type": envelope.Type,
		"data": envelope.Data,
	})
	// Emit interactive events as standalone events for Layout-level dialogs.
	if eventType == "ask_user:request" || eventType == "approval:request" ||
		eventType == "ask_user:cancel" || eventType == "approval:cancel" {
		var parsed interface{}
		if err := json.Unmarshal(data, &parsed); err == nil {
			a.enqueueUIEvent(eventType, parsed)
		}
	}
}

func (a *App) switchWorkspace(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	a.stopShare()
	a.stopIMAdapters()
	if a.chat != nil {
		a.chat.Cancel()
		a.chat.Close()
		a.chat = nil
		wailskit.SetChatBridge(nil)
	}
	a.workDir = dir
	if err := os.Chdir(dir); err != nil {
		return err
	}
	a.initWorkspace(dir)
	return nil
}

// shutdown is called when the app is closing.
func (a *App) shutdown(_ context.Context) {
	a.stopShare()
	a.stopIMAdapters()
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
	if err := a.switchWorkspace(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// InitWorkspace initializes the workspace at the given directory.
func (a *App) InitWorkspace(dir string) error {
	return a.switchWorkspace(dir)
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
	text := userMsg
	safego.Go("wails-send-message", func() {
		if err := a.chat.SendMessage(text); err != nil {
			raw, _ := json.Marshal(map[string]string{"message": err.Error()})
			a.emitStreamEvent("error", raw)
		}
	})
	return nil
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

// IsWorking reports whether the agent loop is currently running.
func (a *App) IsWorking() bool {
	if a.chat == nil {
		return false
	}
	return a.chat.IsWorking()
}

// SetPermissionMode changes the agent permission mode at runtime.
func (a *App) SetPermissionMode(mode string) {
	if a.chat != nil {
		a.chat.SetPermissionMode(mode)
	}
}

// SwitchModel changes the active model at runtime.
func (a *App) SwitchModel(model string) error {
	if a.chat != nil {
		return a.chat.SwitchModel(model)
	}
	return fmt.Errorf("chat not initialized")
}

// GetAvailableModels returns models available for current endpoint.
func (a *App) GetAvailableModels() []string {
	if a.chat != nil {
		return a.chat.GetAvailableModels()
	}
	return nil
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

// GetResolvedEndpoint returns the currently resolved active endpoint info.
func (a *App) GetResolvedEndpoint() (*wailskit.ResolvedEndpointInfo, error) {
	return wailskit.GetResolvedEndpoint()
}

// FetchModels dynamically discovers models from an API endpoint.
func (a *App) FetchModels(vendor, endpoint, apiKey, baseURL string) ([]string, error) {
	return wailskit.FetchModelsForEndpoint(vendor, endpoint, apiKey, baseURL)
}

// GetEndpointDetails returns details for a specific vendor endpoint.
func (a *App) GetEndpointDetails(vendor, endpoint string) *wailskit.EndpointDetails {
	return wailskit.GetEndpointDetails(vendor, endpoint)
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
		a.chat.ClearCurrentSession()
	}
	return nil
}

// LoadSession loads an existing session by ID.
func (a *App) LoadSession(id string) error {
	if a.chat != nil {
		a.chat.Cancel()
		return a.chat.LoadSession(id)
	}
	return fmt.Errorf("chat not initialized")
}

// GetSessionHistory returns messages from the current session.
func (a *App) GetSessionHistory() ([]wailskit.SessionMessage, error) {
	if a.chat == nil {
		return nil, nil
	}
	return a.chat.CurrentSessionHistory(), nil
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

func (a *App) SetMCPServerEnabled(name string, enabled bool) bool {
	return wailskit.SetMCPServerEnabled(name, enabled)
}

func (a *App) ReconnectMCPServer(name string) bool {
	return wailskit.ReconnectMCPServer(name)
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

// CheckForUpdates checks GitHub for the latest release.
func (a *App) CheckForUpdates() (map[string]interface{}, error) {
	svc := update.NewService(a.GetVersion(), "", "", "")
	result, err := svc.Check(a.ctx)
	if err != nil {
		return map[string]interface{}{
			"current_version": a.GetVersion(),
			"error":           err.Error(),
		}, nil
	}
	return map[string]interface{}{
		"current_version": result.CurrentVersion,
		"latest_version":  result.LatestVersion,
		"has_update":      result.HasUpdate,
		"checked_at":      result.CheckedAt.Format(time.RFC3339),
	}, nil
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

// FileBinaryData holds base64-encoded file content with its MIME type.
type FileBinaryData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64 encoded
}

// ReadFileAsBase64 reads a binary file (image, PDF, etc.) and returns base64 data.
func (a *App) ReadFileAsBase64(path string) (*FileBinaryData, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	mime := mimeTypeFromExt(abs)
	return &FileBinaryData{
		MimeType: mime,
		Data:     base64.StdEncoding.EncodeToString(data),
	}, nil
}

func mimeTypeFromExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".bmp":
		return "image/bmp"
	case ".pdf":
		return "application/pdf"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".webm":
		return "video/webm"
	default:
		return "application/octet-stream"
	}
}

// ─── Approval & AskUser ────────────────────────────────────────
// Approval/AskUser handling is delegated to wailskit.ChatBridge.
// See chat.go for the full implementation.

// RespondApproval is called from the frontend when the user responds to an approval request.
func (a *App) RespondApproval(requestID string, decision string) {
	if a.chat != nil {
		a.chat.RespondApproval(requestID, decision)
	}
}

// RespondAskUser is called from the frontend when the user responds to an ask_user request.
func (a *App) RespondAskUser(requestID string, answersJSON string) {
	if a.chat == nil {
		return
	}

	// Frontend sends {"status":"submitted","answers":[...]}
	var payload struct {
		Status  string               `json:"status"`
		Answers []tool.AskUserAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(answersJSON), &payload); err != nil {
		return
	}

	answeredCount := 0
	for _, ans := range payload.Answers {
		if ans.Answered {
			answeredCount++
		}
	}

	response := tool.AskUserResponse{
		Status:        tool.AskUserStatus(payload.Status),
		QuestionCount: len(payload.Answers),
		AnsweredCount: answeredCount,
		Answers:       payload.Answers,
	}
	a.chat.RespondAskUser(requestID, response)
}

// ─── IM Runtime (mirrors Fyne's initIMRuntime / im_bridge.go) ──────────

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

	workDir := ""
	if a.dc != nil {
		workDir = a.dc.WorkDir
	}
	adapters := make(map[string]bool)
	cfg, _ := wailskit.LoadConfigForWorkspace(workDir)
	if cfg != nil && cfg.IM.Adapters != nil {
		for name, acfg := range cfg.IM.Adapters {
			adapters[name] = acfg.Enabled
		}
	}
	runtimeInit, err := im.InitRuntime(im.RuntimeInitOptions{
		Workspace:        workDir,
		EnabledAdapters:  adapters,
		RegisterInstance: workDir != "",
		OnUpdate: func(snap im.StatusSnapshot) {
			// Pairing code dialog
			if snap.PendingPairing != nil {
				ch := snap.PendingPairing
				runtime.EventsEmit(a.ctx, "im:pairing", map[string]string{
					"adapter": ch.Adapter, "platform": string(ch.Platform), "code": ch.Code, "kind": string(ch.Kind),
				})
			} else {
				// Pairing complete — dismiss dialog
				runtime.EventsEmit(a.ctx, "im:pairing_done", map[string]string{})
			}
			// Push status to frontend via both Wails events and stream events
			raw, _ := json.Marshal(snap)
			if a.chat != nil && a.chat.OnStreamEvent != nil {
				a.chat.OnStreamEvent("im:status", raw)
			}
			runtime.EventsEmit(a.ctx, "im:status", map[string]interface{}{
				"adapters": len(snap.Adapters),
			})
		},
	})
	if err != nil {
		return
	}

	a.imManager = runtimeInit.Manager
	// Single OnUpdate callback handles pairing + status + stream event push
	a.imInstanceDetect = runtimeInit.InstanceDetect
	if len(runtimeInit.OtherInstances) > 0 {
		fmt.Printf("im: auto-muted IM channels, another instance is primary\n")
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

	// Bind IM emitter to chat bridge for outbound push
	if a.chat != nil {
		lang := ""
		if cfg != nil {
			lang = cfg.Language
		}
		a.chat.Emitter = im.NewIMEmitter(a.imManager, lang, a.workDir)
	}

	a.imManager.SetBridge(&im.InteractiveTextBridge{
		Submit: func(_ context.Context, text string) error {
			if a == nil || a.chat == nil {
				return fmt.Errorf("app not available")
			}
			safego.Run("im-inbound", func() {
				_ = a.chat.SendMessage(text)
			})
			return nil
		},
		CurrentApproval: func() (string, string, bool) {
			if a == nil || a.chat == nil {
				return "", "", false
			}
			return a.chat.PendingApprovalRequest()
		},
		ResolveApproval: func(requestID, decision string) {
			if a == nil || a.chat == nil {
				return
			}
			a.chat.RespondApproval(requestID, decision)
		},
		CurrentAskUser: func() (string, tool.AskUserRequest, bool) {
			if a == nil || a.chat == nil {
				return "", tool.AskUserRequest{}, false
			}
			return a.chat.PendingAskUserRequest()
		},
		ResolveAskUser: func(requestID string, response tool.AskUserResponse) {
			if a == nil || a.chat == nil {
				return
			}
			a.chat.RespondAskUser(requestID, response)
		},
	})

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

// BindIMAdapter binds an adapter to the current workspace.
func (a *App) BindIMAdapter(name string) error {
	return wailskit.BindIMAdapter(name, a.workDir, a.imManager)
}

// RebindIMAdapter re-binds an adapter to the current workspace.
func (a *App) RebindIMAdapter(name string) error {
	return wailskit.RebindIMAdapter(name, a.workDir, a.imManager)
}

// UnbindIMAdapter removes all bindings for an adapter.
func (a *App) UnbindIMAdapter(name string) error {
	return wailskit.UnbindIMAdapter(name, a.imManager)
}

// ─── Tunnel / Share ──────────────────────────────────────────────────

// ShareInfo is returned to the frontend with connection details.
type ShareInfo struct {
	ConnectURL   string `json:"connectURL"`
	QRCodeBase64 string `json:"qrCodeBase64"`
}

func (a *App) currentTunnelSession() *tunnel.Session {
	a.tunnelMu.RLock()
	defer a.tunnelMu.RUnlock()
	return a.tunnelSession
}

func (a *App) currentTunnelBroker() *tunnel.Broker {
	a.tunnelMu.RLock()
	defer a.tunnelMu.RUnlock()
	return a.tunnelBroker
}

func (a *App) setTunnelState(sess *tunnel.Session, broker *tunnel.Broker) {
	a.tunnelMu.Lock()
	defer a.tunnelMu.Unlock()
	a.tunnelSession = sess
	a.tunnelBroker = broker
}

func (a *App) clearTunnelState() {
	a.tunnelMu.Lock()
	defer a.tunnelMu.Unlock()
	a.tunnelSession = nil
	a.tunnelBroker = nil
}

// IsSharing returns whether a tunnel is active.
func (a *App) IsSharing() bool {
	return a.currentTunnelSession() != nil
}

// StartShare starts a tunnel session and returns connection info for the frontend.
func (a *App) StartShare() (*ShareInfo, error) {
	// If already sharing, refresh the invite
	if sess := a.currentTunnelSession(); sess != nil {
		info, err := sess.RefreshInvite(context.Background())
		if err != nil {
			return nil, fmt.Errorf("refresh invite: %w", err)
		}
		return &ShareInfo{
			ConnectURL:   info.ConnectURL,
			QRCodeBase64: encodeQRBase64(info.QRCodePNG),
		}, nil
	}

	// Start new tunnel session
	sess := tunnel.NewSession(tunnel.DefaultRelayURL, tunnel.WithClientMetadata("desktop-wails", a.GetVersion()))
	info, err := sess.Start(context.Background())
	if err != nil {
		return nil, fmt.Errorf("start tunnel: %w", err)
	}

	broker := tunnel.NewBroker(sess)

	// Notify frontend when mobile client connects (via broker.OnRelayConnected —
	// does NOT override broker's internal handleRelayConnected)
	broker.OnRelayConnected(func(info tunnel.RelayConnectedState) {
		runtime.EventsEmit(a.ctx, "tunnel:connected", map[string]interface{}{
			"role": info.Role, "sessionID": info.SessionID, "generation": info.Generation,
		})
	})

	a.setTunnelState(sess, broker)

	if a.chat != nil {
		a.chat.BindShareCommands(broker, func(language string) {
			cfg, _ := wailskit.LoadConfigForWorkspace(a.workDir)
			if cfg != nil {
				_ = cfg.SaveLanguagePreference(language)
			}
		}, a.currentAskUserRequest, a.clearAskUserRequest)
		a.chat.PrepareShareBroker(broker, func() tunnel.BrokerSnapshot {
			return a.tunnelSnapshot()
		})
	} else {
		// Set snapshot provider for handleRelayConnected callback
		broker.SetSnapshotProvider(func() tunnel.BrokerSnapshot {
			return a.tunnelSnapshot()
		})
	}

	return &ShareInfo{
		ConnectURL:   info.ConnectURL,
		QRCodeBase64: encodeQRBase64(info.QRCodePNG),
	}, nil
}

// StopShare stops the active tunnel session.
func (a *App) StopShare() {
	a.stopShare()
	runtime.EventsEmit(a.ctx, "tunnel:disconnected", nil)
}

func (a *App) stopShare() {
	broker := a.currentTunnelBroker()
	sess := a.currentTunnelSession()
	if a.chat != nil {
		a.chat.DetachTunnelBroker()
	}
	agentruntime.StopSharedTunnelGracefully(sess, broker, 2*time.Second)
	a.clearTunnelState()
}

// onTunnelCommand routes inbound mobile commands to the appropriate handler.
// tunnelSnapshot builds a complete snapshot for the mobile client.
func (a *App) tunnelSnapshot() tunnel.BrokerSnapshot {
	snapshot := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{
			Workspace: a.workDir,
			Version:   a.GetVersion(),
			Language:  a.dc.Language,
		},
	}

	// Populate model/provider from config
	if cfg, err := wailskit.LoadConfigForWorkspace(a.workDir); err == nil {
		snapshot.SessionInfo.Provider = cfg.Vendor
		snapshot.SessionInfo.Model = cfg.Model
		snapshot.SessionInfo.Mode = cfg.DefaultMode
	}

	if a.chat == nil {
		snapshot.Status = tunnel.StatusData{Status: tunnel.StatusIdle}
		return snapshot
	}
	snapshot.Status = a.chat.CurrentTunnelStatus()
	return snapshot
}

// ─── AskUser request state for mobile response mapping ─────────────

// currentAskUserRequest returns the stored ask_user request for mobile response mapping.
func (a *App) currentAskUserRequest() tool.AskUserRequest {
	a.askUserMu.Lock()
	defer a.askUserMu.Unlock()
	return a.askUserReq
}

// clearAskUserRequest clears the stored ask_user request after processing.
func (a *App) clearAskUserRequest() {
	a.askUserMu.Lock()
	defer a.askUserMu.Unlock()
	a.hasAskUserReq = false
	a.askUserReq = tool.AskUserRequest{}
}

// storeAskUserRequest stores the current ask_user request for later mobile response mapping.
func (a *App) storeAskUserRequest(req tool.AskUserRequest) {
	a.askUserMu.Lock()
	defer a.askUserMu.Unlock()
	a.askUserReq = req
	a.hasAskUserReq = true
}

func encodeQRBase64(pngData []byte) string {
	if len(pngData) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData)
}
