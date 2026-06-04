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
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
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

	var answers []tool.AskUserAnswer
	if err := json.Unmarshal([]byte(answersJSON), &answers); err != nil {
		return
	}

	answeredCount := 0
	for _, ans := range answers {
		if ans.Answered {
			answeredCount++
		}
	}

	response := tool.AskUserResponse{
		Status:        tool.AskUserStatusSubmitted,
		QuestionCount: len(answers),
		AnsweredCount: answeredCount,
		Answers:       answers,
	}
	a.chat.RespondAskUser(requestID, response)
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

	// IM status updates via frontend events
	mgr.SetOnUpdate(func(snap im.StatusSnapshot) {
		// Pairing code dialog
		if snap.PendingPairing != nil {
			ch := snap.PendingPairing
			runtime.EventsEmit(a.ctx, "im:pairing", map[string]string{
				"adapter": ch.Adapter, "platform": string(ch.Platform), "code": ch.Code,
			})
		} else {
			// Pairing complete — dismiss dialog
			runtime.EventsEmit(a.ctx, "im:pairing_done", map[string]string{})
		}
		// Adapter status snapshot for IM management page
		runtime.EventsEmit(a.ctx, "im:status", map[string]interface{}{
			"adapters": len(snap.Adapters),
		})
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

	// Handle inbound commands from mobile client
	broker.OnCommand(func(cmd tunnel.GatewayMessage) {
		a.onTunnelCommand(cmd, broker)
	})

	// Notify frontend when mobile client connects (via broker.OnRelayConnected —
	// does NOT override broker's internal handleRelayConnected)
	broker.OnRelayConnected(func(info tunnel.RelayConnectedState) {
		runtime.EventsEmit(a.ctx, "tunnel:connected", map[string]interface{}{
			"role": info.Role, "sessionID": info.SessionID, "generation": info.Generation,
		})
	})

	a.setTunnelState(sess, broker)

	// Ensure session exists before attaching broker
	if a.chat != nil {
		a.chat.EnsureSession()
	}

	// Set snapshot provider for handleRelayConnected callback
	broker.SetSnapshotProvider(func() tunnel.BrokerSnapshot {
		return a.tunnelSnapshot()
	})

	// AttachTunnelBroker does everything: bindTunnelProjectionSession,
	// SwitchSession, SendSnapshot, SetReplayProvider, BindSession,
	// SetAuthorityEpoch, AnnounceActiveSession, SendSessionInfo,
	// PushStatus, PushActivity — mirrors Fyne exactly.
	if a.chat != nil {
		a.chat.AttachTunnelBroker(broker)
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
	if broker != nil {
		broker.StopSharingGracefully(2 * time.Second)
	} else if sess != nil {
		sess.DestroyGracefully(2 * time.Second)
	}
	a.clearTunnelState()
}

// onTunnelCommand routes inbound mobile commands to the appropriate handler.
func (a *App) onTunnelCommand(cmd tunnel.GatewayMessage, broker *tunnel.Broker) {
	switch cmd.Type {
	case "user_text", "message":
		var data tunnel.MessageData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		text := strings.TrimSpace(data.Text)
		if text == "" {
			return
		}
		// Acknowledge to mobile
		broker.PushServerAck(tunnel.NormalizeClientMessageID(data.MessageID))
		// Forward to agent
		safego.Run("tunnel-inbound", func() {
			if a.chat != nil {
				_ = a.chat.SendMessage(text)
			}
		})

	case tunnel.CmdApprovalResponse:
		var data tunnel.ApprovalResponseData
		if err := json.Unmarshal(cmd.Data, &data); err == nil {
			if a.chat != nil {
				a.chat.HandleMobileApprovalResponse(data)
			}
		}

	case tunnel.CmdAskUserResponse:
		var data tunnel.AskUserResponseData
		if err := json.Unmarshal(cmd.Data, &data); err == nil {
			if a.chat != nil {
				req := a.currentAskUserRequest()
				a.chat.HandleMobileAskUserResponse(data, req)
				a.clearAskUserRequest()
			}
		}

	case tunnel.CmdInterrupt:
		if a.chat != nil {
			a.chat.Cancel()
		}

	case tunnel.CmdLanguageChange:
		var data tunnel.LanguageChangeData
		if err := json.Unmarshal(cmd.Data, &data); err == nil && data.Language != "" {
			cfg, _ := wailskit.LoadConfigForWorkspace(a.workDir)
			if cfg != nil {
				_ = cfg.SaveLanguagePreference(data.Language)
			}
		}

	case tunnel.CmdThemeChange:
		// Theme not yet supported in Wails desktop
	}
}

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
