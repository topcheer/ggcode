package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	imgpkg "github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/lanchat"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/version"
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
	shutdownOnce sync.Once

	// Runtime debug log stream
	logStream   *wailskit.LogStream
	streamMu    sync.Mutex
	streamQueue []StreamEventEnvelope
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
	chat.OnSessionChanged = func() {
		a.bindCurrentIMSession()
	}
	chat.EmitEvent = func(name string, payload ...interface{}) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, name, payload...)
		}
	}
	a.chat = chat
	wailskit.SetChatBridge(chat)

	// Initialize log stream and hook to debug.Log
	a.logStream = wailskit.NewLogStream(2000)
	debug.SetLiveSink(func(category, msg string) {
		a.logStream.Write(category, msg)
	})

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

// ToggleLogStream enables or disables the runtime debug log stream.
func (a *App) ToggleLogStream(enabled bool) {
	if a.logStream != nil {
		a.logStream.ToggleLogStream(enabled)
	}
}

// DrainLogStream returns new log entries since last call as JSON string.
func (a *App) DrainLogStream() string {
	if a.logStream == nil {
		return "[]"
	}
	return a.logStream.DrainLogStreamJSON()
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
	if eventType == "pending_consumed" {
		a.enqueueUIEvent(eventType, nil)
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
	a.shutdownOnce.Do(func() {
		a.stopShare()
		a.stopIMAdapters()
		if a.chat != nil {
			a.chat.Cancel()
		}
	})
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

type PastedImage struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
	Name     string `json:"name,omitempty"`
}

type ClipboardAttachment struct {
	Path     string `json:"path,omitempty"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType,omitempty"`
	Kind     string `json:"kind"`
	Content  string `json:"content,omitempty"`
	Data     string `json:"data,omitempty"`
	Error    string `json:"error,omitempty"`
}

const maxClipboardFileBytes int64 = 10 * 1024 * 1024

func (a *App) ReadClipboardImage() (*PastedImage, error) {
	img, err := imgpkg.ReadClipboard()
	if err != nil {
		if errors.Is(err, imgpkg.ErrClipboardImageUnavailable) {
			return nil, nil
		}
		return nil, err
	}
	return &PastedImage{
		MimeType: img.MIME,
		Data:     imgpkg.EncodeBase64(img),
		Name:     "clipboard-image",
	}, nil
}

func (a *App) ReadClipboardAttachments() ([]ClipboardAttachment, error) {
	paths, err := clipboardFilePaths()
	if err != nil {
		return nil, err
	}
	attachments := make([]ClipboardAttachment, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			attachments = append(attachments, ClipboardAttachment{Path: path, Name: filepath.Base(path), Kind: "binary", Error: fmt.Sprintf("resolve path: %v", err)})
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		attachments = append(attachments, readClipboardFileAttachment(abs))
	}
	return attachments, nil
}

func clipboardFilePaths() ([]string, error) {
	script := `use framework "AppKit"
use scripting additions
set pb to current application's NSPasteboard's generalPasteboard()
set urls to pb's readObjectsForClasses:{current application's NSURL} options:{NSPasteboardURLReadingFileURLsOnlyKey:true}
set out to {}
if urls is not missing value then
	repeat with u in urls
		set p to (u's |path|()) as text
		if p is not missing value then set end of out to p
	end repeat
end if
return out as text`
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	return parseClipboardPathOutput(string(output)), nil
}

func parseClipboardPathOutput(output string) []string {
	var paths []string
	for _, part := range strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n") {
		for _, item := range strings.Split(part, ", ") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if strings.HasPrefix(item, "file://") {
				if u, err := url.Parse(item); err == nil {
					item = u.Path
				}
			}
			paths = append(paths, item)
		}
	}
	return paths
}

func readClipboardFileAttachment(path string) ClipboardAttachment {
	att := ClipboardAttachment{
		Path: path,
		Name: filepath.Base(path),
		Kind: "binary",
	}
	info, err := os.Stat(path)
	if err != nil {
		att.Error = fmt.Sprintf("stat file: %v", err)
		return att
	}
	att.Size = info.Size()
	if info.IsDir() {
		att.Error = "Directories are not supported yet"
		return att
	}
	if info.Size() > maxClipboardFileBytes {
		att.Error = "File is larger than 10MB"
		return att
	}

	if img, err := imgpkg.ReadFile(path); err == nil {
		att.Kind = "image"
		att.MimeType = img.MIME
		att.Data = imgpkg.EncodeBase64(img)
		return att
	}

	data, err := os.ReadFile(path)
	if err != nil {
		att.Error = fmt.Sprintf("read file: %v", err)
		return att
	}
	att.MimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if looksLikeText(data) {
		att.Kind = "text"
		if att.MimeType == "" {
			att.MimeType = "text/plain; charset=utf-8"
		}
		att.Content = string(data)
		return att
	}
	if att.MimeType == "" {
		att.MimeType = "application/octet-stream"
	}
	att.Error = "Binary files are not pasted as text"
	return att
}

func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if !utf8.Valid(data) {
		return false
	}
	controls := 0
	for _, b := range data {
		if b == 0 {
			return false
		}
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			controls++
		}
	}
	return controls*100/len(data) < 5
}

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

// LanChatParticipants returns all known LAN chat participants.
func (a *App) LanChatParticipants() ([]lanchat.Participant, error) {
	if a.chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return a.chat.LanChatParticipants()
}

// LanChatMessages returns recent LAN chat messages.
func (a *App) LanChatMessages() ([]lanchat.Message, error) {
	if a.chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return a.chat.LanChatMessages()
}

// LanChatSend sends a LAN chat message (broadcast if toNodeID is empty).
func (a *App) LanChatSend(content, toNodeID, toRole string, asAgent bool) error {
	if a.chat == nil {
		return fmt.Errorf("chat not available")
	}
	return a.chat.LanChatSend(content, toNodeID, toRole, asAgent)
}

// LanChatSetNick changes the user's nickname.
func (a *App) LanChatSetNick(nick string) error {
	if a.chat == nil {
		return fmt.Errorf("chat not available")
	}
	return a.chat.LanChatSetNick(nick)
}

// LanChatPendingApprovals returns pending @agent messages.
func (a *App) LanChatPendingApprovals() ([]lanchat.PendingAgentMsg, error) {
	if a.chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return a.chat.LanChatPendingApprovals()
}

// LanChatApprove approves a pending @agent message.
func (a *App) LanChatApprove(messageID string) error {
	if a.chat == nil {
		return fmt.Errorf("chat not available")
	}
	return a.chat.LanChatApprove(messageID)
}

// LanChatReject rejects a pending @agent message.
func (a *App) LanChatReject(messageID, reason string) error {
	if a.chat == nil {
		return fmt.Errorf("chat not available")
	}
	return a.chat.LanChatReject(messageID, reason)
}

// LanChatSelf returns this node's own participant info.
func (a *App) LanChatSelf() (lanchat.Participant, error) {
	if a.chat == nil {
		return lanchat.Participant{}, fmt.Errorf("chat not available")
	}
	return a.chat.LanChatSelf()
}

func (a *App) SendMessageWithImages(userMsg string, images []PastedImage) error {
	if a.chat == nil {
		return nil
	}
	text := strings.TrimSpace(userMsg)
	imgs := append([]PastedImage(nil), images...)
	safego.Go("wails-send-message-images", func() {
		content := make([]provider.ContentBlock, 0, 1+len(imgs))
		if text != "" {
			content = append(content, provider.TextBlock(text))
		}
		for _, img := range imgs {
			mime := strings.TrimSpace(img.MimeType)
			if mime == "" {
				mime = "image/png"
			}
			data := strings.TrimSpace(img.Data)
			if idx := strings.Index(data, ","); strings.HasPrefix(data, "data:") && idx >= 0 {
				data = data[idx+1:]
			}
			if data == "" {
				continue
			}
			content = append(content, provider.ImageBlock(mime, data))
		}
		if len(content) == 0 {
			return
		}
		if err := a.chat.SendContent(content); err != nil {
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

func (a *App) CycleReasoningEffort() (map[string]interface{}, error) {
	if a.chat == nil {
		return map[string]interface{}{"effort": "auto", "supported": false}, nil
	}
	effort, supported := a.chat.CycleReasoningEffort()
	return map[string]interface{}{"effort": effort, "supported": supported}, nil
}

func (a *App) GetTeamBoard() []swarm.TeamBoardSnapshot {
	if a.chat == nil {
		return []swarm.TeamBoardSnapshot{}
	}
	return a.chat.GetTeamBoard()
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
	if err := wailskit.UpdateConfig(values); err != nil {
		return err
	}
	// Refresh provider so the running agent uses the new LLM backend
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		bridge.OnConfigProviderChanged()
	}
	return nil
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

// NewSession creates a fresh initialized session, cancelling any current work.
func (a *App) NewSession() (string, error) {
	if a.chat == nil {
		return "", nil
	}
	a.chat.Cancel()
	a.stopShareForSessionChange()
	return a.chat.StartNewSession()
}

// LoadSession loads an existing session by ID.
func (a *App) LoadSession(id string) error {
	if a.chat != nil {
		a.chat.Cancel()
		a.stopShareForSessionChange()
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

// ForceReauthMCPServer deletes the per-server OAuth credential and triggers
// a fresh OAuth flow for the named MCP server.
func (a *App) ForceReauthMCPServer(name string) bool {
	return wailskit.ForceReauthMCPServer(name)
}

func (a *App) StartMCPOAuth(name string) (*wailskit.MCPOAuthStartResult, error) {
	if a.chat == nil {
		return nil, fmt.Errorf("chat not initialized")
	}
	return a.chat.StartMCPOAuth(a.ctx, name, func(url string) error {
		runtime.BrowserOpenURL(a.ctx, url)
		return nil
	})
}

func (a *App) CompleteMCPOAuth(name string) error {
	if a.chat == nil {
		return fmt.Errorf("chat not initialized")
	}
	return a.chat.CompleteMCPOAuth(a.ctx, name)
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
	return version.Version
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
		Status:        payload.Status,
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
		debug.Log("desktop", "IM start: manager not initialized, skipping")
		return
	}
	cfg, _ := wailskit.LoadConfigForWorkspace(a.workDir)
	if cfg == nil || !cfg.IM.Enabled {
		debug.Log("desktop", "IM start: disabled in config, skipping")
		return
	}
	debug.Log("desktop", "IM start: initializing adapters for workspace=%s", a.workDir)

	// Bind IM emitter to chat bridge for outbound push
	if a.chat != nil {
		lang := ""
		if cfg != nil {
			lang = cfg.Language
		}
		a.chat.Emitter = im.NewIMEmitter(a.imManager, lang, a.workDir)
	}

	a.imManager.SetBridge(&im.InteractiveTextBridge{
		Submit: func(_ context.Context, text string, adapterName string) error {
			if a == nil || a.chat == nil {
				return fmt.Errorf("app not available")
			}
			safego.Run("im-inbound", func() {
				_ = a.chat.SendNonUIMessage(text, "im", adapterName)
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
		debug.Log("desktop", "IM start failed: %v", err)
		fmt.Printf("IM adapter start error: %v\n", err)
		return
	}
	a.imController = controller
	debug.Log("desktop", "IM start: adapter controller started successfully")
}

// stopIMAdapters stops all running IM adapters.
func (a *App) stopIMAdapters() {
	debug.Log("desktop", "IM stop: shutting down adapters")
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

// GetLSPStatus returns detected language server status for the current workspace.
func (a *App) GetLSPStatus() wailskit.LSPStatusResponse {
	if a.chat == nil {
		return wailskit.LSPStatusResponse{}
	}
	return a.chat.GetLSPStatus()
}

// InstallLSPServer installs a language server for the given language.
func (a *App) InstallLSPServer(languageID, optionID string) wailskit.LSPInstallResult {
	if a.chat == nil {
		return wailskit.LSPInstallResult{Success: false, Output: "chat bridge not initialized"}
	}
	return a.chat.InstallLSPServer(languageID, optionID)
}

// SaveIMAdapter creates or updates an IM adapter.
func (a *App) SaveIMAdapter(name string, values map[string]string) error {
	debug.Log("desktop", "IM SaveAdapter: name=%s platform=%s", name, values["platform"])
	// Stop existing adapter if running (for config updates)
	a.imStopAdapter(name)
	err := wailskit.SaveIMAdapter(name, values)
	if err != nil {
		debug.Log("desktop", "IM SaveAdapter failed: %v", err)
		return err
	}
	// Auto-start if enabled
	if values["enabled"] != "false" {
		name := name
		safego.Go("desktop.im-start-save", func() { a.imStartAdapter(name) })
	}
	return nil
}

// RemoveIMAdapter removes an IM adapter by name.
func (a *App) RemoveIMAdapter(name string) error {
	debug.Log("desktop", "IM RemoveAdapter: name=%s", name)
	a.imStopAdapter(name)
	err := wailskit.RemoveIMAdapter(name)
	if err != nil {
		debug.Log("desktop", "IM RemoveAdapter failed: %v", err)
	}
	return err
}

// imStartAdapter starts a single adapter by name in the background.
func (a *App) bindCurrentIMSession() {
	if a.imManager == nil || a.chat == nil {
		return
	}
	if ses := a.chat.CurrentSession(); ses != nil {
		a.imManager.BindSession(im.SessionBinding{
			SessionID: ses.ID,
			Workspace: a.workDir,
		})
	}
}

func (a *App) imStartAdapter(name string) {
	if a.imManager == nil {
		debug.Log("desktop", "IM start %s: manager not initialized", name)
		return
	}
	cfg, _ := wailskit.LoadConfigForWorkspace(a.workDir)
	if cfg == nil {
		debug.Log("desktop", "IM start %s: no config", name)
		return
	}
	// Ensure session is bound so pairing and inbound work
	a.bindCurrentIMSession()
	debug.Log("desktop", "IM start: starting adapter %s", name)
	if err := im.StartNamedAdapter(context.Background(), cfg.IM, name, a.imManager); err != nil {
		debug.Log("desktop", "IM start %s failed: %v", name, err)
	} else {
		debug.Log("desktop", "IM start %s: ok", name)
	}
}

// imStopAdapter stops a single adapter by name.
func (a *App) imStopAdapter(name string) {
	if a.imManager == nil {
		debug.Log("desktop", "IM stop %s: manager not initialized", name)
		return
	}
	debug.Log("desktop", "IM stop: stopping adapter %s", name)
	a.imManager.StopAdapter(name)
	debug.Log("desktop", "IM stop %s: ok", name)
}

// SetIMAdapterEnabled enables or disables an IM adapter.
func (a *App) SetIMAdapterEnabled(name string, enabled bool) error {
	debug.Log("desktop", "IM SetEnabled: name=%s enabled=%v", name, enabled)
	err := wailskit.SetIMAdapterEnabled(name, enabled)
	if err != nil {
		debug.Log("desktop", "IM SetEnabled failed: %v", err)
		return err
	}
	if enabled {
		name := name
		safego.Go("desktop.im-start-enabled", func() { a.imStartAdapter(name) })
	} else {
		a.imStopAdapter(name)
	}
	return nil
}

// MuteIMAdapter mutes or unmutes an adapter channel.
// Muting stops the adapter runtime; unmuting restarts it.
func (a *App) MuteIMAdapter(name string, muted bool) error {
	debug.Log("desktop", "IM Mute: name=%s muted=%v", name, muted)
	if a.imManager == nil {
		debug.Log("desktop", "IM Mute failed: IM not initialized")
		return fmt.Errorf("IM not initialized")
	}
	if muted {
		// Stop the adapter
		if err := a.imManager.MuteBinding(name); err != nil {
			debug.Log("desktop", "IM MuteBinding failed: %v", err)
			return err
		}
		a.imStopAdapter(name)
	} else {
		// Unmute and restart
		if err := a.imManager.UnmuteBinding(name); err != nil {
			debug.Log("desktop", "IM UnmuteBinding failed: %v", err)
			return err
		}
		name := name
		safego.Go("desktop.im-start-unmute", func() { a.imStartAdapter(name) })
	}
	return nil
}

// BindIMAdapter binds an adapter to the current workspace.
func (a *App) BindIMAdapter(name string) error {
	debug.Log("desktop", "IM Bind: name=%s workDir=%s", name, a.workDir)
	err := wailskit.BindIMAdapter(name, a.workDir, a.imManager)
	if err != nil {
		debug.Log("desktop", "IM Bind failed: %v", err)
		return err
	}
	// Start the adapter after binding
	name := name
	safego.Go("desktop.im-start-bind", func() { a.imStartAdapter(name) })
	return nil
}

// RebindIMAdapter re-binds an adapter to the current workspace.
func (a *App) RebindIMAdapter(name string) error {
	debug.Log("desktop", "IM Rebind: name=%s workDir=%s", name, a.workDir)
	a.imStopAdapter(name)
	err := wailskit.RebindIMAdapter(name, a.workDir, a.imManager)
	if err != nil {
		debug.Log("desktop", "IM Rebind failed: %v", err)
		return err
	}
	name := name
	safego.Go("desktop.im-start-rebind", func() { a.imStartAdapter(name) })
	return nil
}

// UnbindIMAdapter removes all bindings for an adapter.
func (a *App) UnbindIMAdapter(name string) error {
	debug.Log("desktop", "IM Unbind: name=%s", name)
	a.imStopAdapter(name)
	err := wailskit.UnbindIMAdapter(name, a.imManager)
	if err != nil {
		debug.Log("desktop", "IM Unbind failed: %v", err)
	}
	return err
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

func (a *App) isSharing() bool {
	a.tunnelMu.RLock()
	defer a.tunnelMu.RUnlock()
	return a.tunnelSession != nil || a.tunnelBroker != nil
}

func (a *App) stopShareForSessionChange() {
	if !a.isSharing() {
		return
	}
	a.stopShare()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "tunnel:disconnected", nil)
		runtime.EventsEmit(a.ctx, "tunnel:session_changed", map[string]string{
			"message": "Mobile sharing was stopped because the session changed. Scan again to reconnect.",
		})
	}
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
	// If already sharing, try to refresh the invite (same room, new ticket).
	// This allows mobile to reconnect seamlessly after a brief relay hiccup.
	if sess := a.currentTunnelSession(); sess != nil {
		info, err := sess.RefreshInvite(context.Background())
		if err != nil {
			// Stale session (room not live, relay restarted, etc.) — discard
			// and create a fresh one below.
			debug.Log("share", "refresh invite failed, starting new session: %v", err)
			a.tunnelMu.Lock()
			a.tunnelSession = nil
			a.tunnelMu.Unlock()
		} else {
			return &ShareInfo{
				ConnectURL:   info.ConnectURL,
				QRCodeBase64: encodeQRBase64(info.QRCodePNG),
			}, nil
		}
	}

	// Resolve config for session info
	cfg, _ := wailskit.LoadConfigForWorkspace(a.workDir)
	model := ""
	vendorName := ""
	mode := ""
	if cfg != nil {
		resolved, _ := cfg.ResolveActiveEndpoint()
		if resolved != nil {
			model = resolved.Model
			vendorName = resolved.VendorName
		}
		mode = cfg.DefaultMode
	}

	// Use unified TunnelHost.StartShare — the single canonical entry point
	// for all frontends. It handles session creation, broker setup,
	// SetSessionInfo, PrepareOnlineShare, and AnnounceActiveSession.
	th := a.chat.GetTunnelHost()
	if th == nil {
		return nil, fmt.Errorf("tunnel host not initialized")
	}

	result, err := th.StartShare(agentruntime.ShareConfig{
		Workspace: a.workDir,
		Model:     model,
		Provider:  vendorName,
		Mode:      mode,
		Version:   a.GetVersion(),
		ClientTag: "desktop-wails",
		SnapshotProvider: func() tunnel.BrokerSnapshot {
			return a.tunnelSnapshot()
		},
		OnConnected: func(info tunnel.RelayConnectedState) {
			if info.Role == "client" {
				runtime.EventsEmit(a.ctx, "tunnel:connected", map[string]interface{}{
					"role": info.Role, "sessionID": info.SessionID, "generation": info.Generation,
				})
			}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("start share: %w", err)
	}

	// Wire share commands (OnCommand handler, language switching, ask_user approval)
	if a.chat != nil && result.Broker != nil {
		a.chat.BindShareCommands(result.Broker, func(language string) {
			c, _ := wailskit.LoadConfigForWorkspace(a.workDir)
			if c != nil {
				_ = c.SaveLanguagePreference(language)
			}
		}, a.currentAskUserRequest, a.clearAskUserRequest)
	}

	a.setTunnelState(result.Session, result.Broker)

	return &ShareInfo{
		ConnectURL:   result.ConnectURL,
		QRCodeBase64: encodeQRBase64(result.QRCodePNG),
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

	// Populate history from agent messages — same as TUI does.
	// Without this, mobile clients receive an empty snapshot for
	// sessions whose projection store is empty.
	msgs := a.chat.Messages()
	if len(msgs) > 0 {
		snapshot.History = messagesToTunnelHistory(msgs)
	}

	return snapshot
}

// messagesToTunnelHistory converts provider messages to tunnel history entries.
// This mirrors the TUI's tunnelMessagesToHistory function so mobile clients
// receive the same conversation snapshot regardless of host frontend.
func messagesToTunnelHistory(msgs []provider.Message) []tunnel.HistoryEntry {
	var history []tunnel.HistoryEntry
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			var textParts []string
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						textParts = append(textParts, strings.TrimSpace(block.Text))
					}
				case "tool_result":
					result := truncateRunesDesktop(block.Output, 500, "...")
					history = append(history, tunnel.HistoryEntry{
						Role:     "tool_result",
						ToolID:   block.ToolID,
						ToolName: block.ToolName,
						Result:   result,
						IsError:  block.IsError,
					})
				}
			}
			if len(textParts) > 0 {
				history = append(history, tunnel.HistoryEntry{
					Role:    "user",
					Content: strings.Join(textParts, "\n"),
				})
			}
		case "assistant":
			for _, block := range msg.Content {
				if reasoning := tunnel.NormalizeReasoningChunk(block.ReasoningContent); reasoning != "" {
					history = append(history, tunnel.HistoryEntry{
						Role:    "reasoning",
						Content: reasoning,
					})
				}
				if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
					history = append(history, tunnel.HistoryEntry{
						Role:    "assistant",
						Content: strings.TrimSpace(block.Text),
					})
				} else if block.Type == "tool_use" {
					argsStr := truncateRunesDesktop(string(block.Input), 200, "...")
					history = append(history, tunnel.HistoryEntry{
						Role:     "tool_call",
						ToolID:   block.ToolID,
						ToolName: block.ToolName,
						ToolArgs: argsStr,
					})
				}
			}
		case "tool":
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					result := truncateRunesDesktop(block.Output, 500, "...")
					history = append(history, tunnel.HistoryEntry{
						Role:     "tool_result",
						ToolID:   block.ToolID,
						ToolName: block.ToolName,
						Result:   result,
						IsError:  block.IsError,
					})
				}
			}
		}
	}
	return history
}

func truncateRunesDesktop(s string, maxRunes int, suffix string) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + suffix
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
