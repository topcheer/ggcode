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
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/desktop/wailskit"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	imgpkg "github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/lanchat"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/version"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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

	notifications *NotificationManager

	// Close-to-tray support
	lastCloseAttempt *time.Time

	streamEvents chan uiEvent
	streamOnce   sync.Once
	shutdownOnce sync.Once

	// Runtime debug log stream
	logStream *wailskit.LogStream
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

	// Initialize system tray icon with quick actions.
	a.initSystemTray()

	// Register system-wide global hotkey (Option+Cmd+G) to toggle window.
	// NOTE: must run after a.dc is loaded below — initGlobalHotkey reads
	// a.dc.IsGlobalHotkeyEnabled() and silently returns when a.dc is nil, so
	// calling it earlier meant the persisted hotkey preference never took
	// effect after a restart (#361).

	// Register native file drag-and-drop handler.
	// When the user drags files from the OS file manager into the window,
	// Wails provides the absolute file paths. We emit them as a frontend
	// event so ChatView can insert file references into the input.
	wailsruntime.OnFileDrop(ctx, func(x, y int, paths []string) {
		if len(paths) == 0 {
			return
		}
		debug.Log("desktop", "file-drop: %d file(s) dropped at (%d,%d)", len(paths), x, y)
		a.enqueueUIEvent("file:drop", map[string]interface{}{
			"x":     x,
			"y":     y,
			"paths": paths,
		})
	})

	// Initialize notification manager
	a.notifications = NewNotificationManager()
	a.notifications.SetContext(ctx)

	// Load shared desktop config (same file as Fyne desktop)
	a.dc = wailskit.LoadDesktopConfig()

	// Register global hotkey now that the config (and its enabled flag) is
	// available. This placement is before the onboarding early-return below,
	// so the hotkey registers on every startup path.
	a.initGlobalHotkey()

	// Sync notification preference from config
	a.notifications.SetEnabled(a.dc.IsNotificationsEnabled())

	// Restore window position and size from the saved desktop config.
	// This makes the window reopen at the same location/size as last session.
	if a.dc.WindowW > 0 && a.dc.WindowH > 0 {
		wailsruntime.WindowSetSize(ctx, a.dc.WindowW, a.dc.WindowH)
	}
	if a.dc.WindowX != 0 || a.dc.WindowY != 0 {
		wailsruntime.WindowSetPosition(ctx, a.dc.WindowX, a.dc.WindowY)
	}
	if a.dc.WindowMax {
		wailsruntime.WindowMaximise(ctx)
	}

	// Restore always-on-top state from saved config.
	if a.dc.IsAlwaysOnTop() {
		wailsruntime.WindowSetAlwaysOnTop(ctx, true)
	}

	// Restore last workspace — but verify it still exists.
	// Desktop uses a cached workspace path; if the directory was moved or
	// deleted since last run, we must not silently continue with a stale path.
	if a.dc.WorkDir != "" {
		if info, err := os.Stat(a.dc.WorkDir); err == nil && info.IsDir() {
			a.workDir = a.dc.WorkDir
			_ = os.Chdir(a.workDir)
		} else {
			// Cached workspace no longer exists — fall back to home dir
			// and clear the stale cache so we don't keep trying.
			debug.Log("desktop", "cached workspace %q no longer exists, falling back to home", a.dc.WorkDir)
			a.workDir = config.HomeDir()
			_ = os.Chdir(a.workDir)
			a.dc.WorkDir = ""
		}
	} else {
		// No cached workspace — default to the user's home directory.
		// Using os.Getwd() would inherit the terminal's CWD, which often
		// has an existing ggcode.yaml (e.g. the repo root) that masks the
		// intended HOME-based config. This is especially important for
		// onboarding: with HOME=/tmp/test-home, the desktop should see no
		// config and show the onboarding wizard.
		a.workDir = config.HomeDir()
		_ = os.Chdir(a.workDir)
	}

	// Check if onboarding is needed before initializing workspace.
	// If the user has no API key configured, skip workspace init and let
	// the frontend show the onboarding wizard. The frontend will call
	// SwitchWorkspace (→ initWorkspace) after onboarding completes.
	cfg, err := wailskit.LoadConfigForWorkspace(a.workDir)
	if err != nil || cfg == nil || cfg.NeedsOnboard() {
		debug.Log("desktop", "onboarding needed, skipping workspace init")
		// Set partial config so frontend GetConfig returns NeedsSetup=true
		if cfg != nil {
			wailskit.SetConfig(cfg)
		}
		return
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
				wailsruntime.EventsEmit(a.ctx, ev.name, ev.payload)
			}
		})
	})
}

func (a *App) enqueueUIEvent(name string, payload interface{}) {
	if a.streamEvents == nil {
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, name, payload)
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
	if err := a.dc.Save(); err != nil {
		debug.Log("desktop", "persist workdir failed: %v", err)
	}

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
		if a.ctx != nil && chat != nil {
			wailsruntime.EventsEmit(a.ctx, "session:changed", map[string]string{
				"sessionId": chat.CurrentSessionID(),
			})
		}
	}
	chat.EmitEvent = func(name string, payload ...interface{}) {
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, name, payload...)
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

	// Try to resume the latest session for this workspace, matching TUI behavior.
	// If no sessions exist, create a new one.
	if latestID := a.resumeLatestSession(); latestID != "" {
		debug.Log("app", "resumed latest session: %s", latestID)
		// InitAgent is already called inside LoadSession→resumeLatestSession,
		// so we skip it here to avoid starting a duplicate A2A server.
	} else {
		chat.EnsureSession()
		_ = chat.InitAgent()
	}

	// Start IM adapters AFTER InitAgent so the bridge has the correct chat instance
	a.startIMAdapters()

	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "workspace:changed", map[string]interface{}{
			"workDir": dir,
		})
		wailsruntime.EventsEmit(a.ctx, "config:updated", nil)
	}
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
	// The streamQueue/DrainStreamEvents channel is dead code — no frontend
	// caller ever drains it — and appending every token delta leaked memory
	// for the app's lifetime (#208). Events go out via chat:stream only.
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

	// Trigger desktop notifications for important events when the
	// window is not focused (user switched to another app).
	if a.notifications != nil {
		switch eventType {
		case "complete":
			a.notifications.Notify("GGCode", "Task completed")
		case "error":
			a.notifications.Notify("GGCode", "An error occurred")
		case "approval:request":
			a.notifications.NotifyApprovalNeeded("GGCode", "Approval needed")
		case "ask_user:request":
			a.notifications.NotifyApprovalNeeded("GGCode", "Question from agent")
		}
	}
}

func (a *App) switchWorkspace(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	// Verify target directory exists BEFORE tearing down the current workspace.
	// This prevents destroying the current session/IM state when the user
	// picks a non-existent path.
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("workspace directory does not exist: %s", dir)
	}

	a.stopShare()
	a.stopIMAdapters()
	if a.chat != nil {
		a.chat.Cancel()
		a.chat.Close()
		a.chat = nil
		wailskit.SetChatBridge(nil)
	}
	// chdir is guaranteed to succeed because we verified the dir above.
	a.workDir = dir
	_ = os.Chdir(dir)
	a.initWorkspace(dir)
	return nil
}

// shutdown is called when the app is closing.
func (a *App) shutdown(_ context.Context) {
	a.shutdownOnce.Do(func() {
		// Persist window position/size so it can be restored on next launch.
		if a.ctx != nil {
			w, h := wailsruntime.WindowGetSize(a.ctx)
			x, y := wailsruntime.WindowGetPosition(a.ctx)
			maximized := wailsruntime.WindowIsMaximised(a.ctx)
			a.dc.SetWindowState(w, h, x, y, maximized)
			if err := a.dc.Save(); err != nil {
				debug.Log("desktop", "persist window-state on shutdown failed: %v", err)
			}
		}
		a.stopShare()
		a.stopIMAdapters()
		a.removeGlobalHotkey()
		a.removeSystemTray()
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
	dir, err := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
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
	if runtime.GOOS != "darwin" {
		// #425: the AppKit osascript path is macOS-only. Previously this ran
		// on every platform, where osascript doesn't exist and the error was
		// swallowed (nil, nil) — pasting files silently did nothing on
		// Windows/Linux. Return an explicit unsupported error instead of
		// masquerading as "clipboard has no files".
		return nil, fmt.Errorf("clipboard file reading is not supported on %s", runtime.GOOS)
	}
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
-- Join with linefeed instead of AppleScript's default ", " so file names
-- containing commas are not split. Save and restore original delimiters.
set savedDelimiters to AppleScript's text item delimiters
set AppleScript's text item delimiters to {linefeed}
set result to out as text
set AppleScript's text item delimiters to savedDelimiters
return result`
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		// #425: surface the failure (e.g. TCC automation-permission denial)
		// instead of swallowing it — callers could not distinguish "clipboard
		// empty" from "osascript failed".
		return nil, fmt.Errorf("reading clipboard file paths via osascript: %w", err)
	}
	return parseClipboardPathOutput(string(output)), nil
}

func parseClipboardPathOutput(output string) []string {
	var paths []string
	// Split only by newlines. The AppleScript above joins the path list with
	// linefeed, so no ", " secondary split is needed (and it would corrupt
	// file names containing ", ").
	for _, item := range strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n") {
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
	// #459: post-read length recheck — Stat().Size() is 0 on FIFOs, so the
	// pre-read check passed deterministically and ReadFile pulled in
	// unbounded data. The recheck caps actual damage at detection time.
	if int64(len(data)) > maxClipboardFileBytes {
		att.Error = "File is larger than 10MB"
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
	// Snapshot under the nil check and use the local inside the goroutine:
	// re-reading a.chat after the check was a TOCTOU race with
	// switchWorkspace, and returning nil silently swallowed the message
	// (#210). Same error convention as the other chat methods.
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	text := userMsg
	safego.Go("wails-send-message", func() {
		if err := chat.SendMessage(text); err != nil {
			raw, _ := json.Marshal(map[string]string{"message": err.Error()})
			a.emitStreamEvent("error", raw)
		}
	})
	return nil
}

// LanChatParticipants returns all known LAN chat participants.
func (a *App) LanChatParticipants() ([]lanchat.Participant, error) {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return chat.LanChatParticipants()
}

// LanChatMessages returns recent LAN chat messages.
func (a *App) LanChatMessages() ([]lanchat.Message, error) {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return chat.LanChatMessages()
}

// LanChatSend sends a LAN chat message (broadcast if toNodeID is empty).
func (a *App) LanChatSend(content, toNodeID, toRole string, asAgent bool) error {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatSend(content, toNodeID, toRole, asAgent)
}

// LanChatSetNick changes the user's nickname.
func (a *App) LanChatSetNick(nick string) error {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatSetNick(nick)
}

// LanChatPendingApprovals returns pending @agent messages.
func (a *App) LanChatPendingApprovals() ([]lanchat.PendingAgentMsg, error) {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return chat.LanChatPendingApprovals()
}

// LanChatApprove approves a pending @agent message.
func (a *App) LanChatApprove(messageID string) error {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatApprove(messageID)
}

// LanChatReject rejects a pending @agent message.
func (a *App) LanChatReject(messageID, reason string) error {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatReject(messageID, reason)
}

// LanChatSelf returns this node's own participant info.
func (a *App) LanChatSelf() (lanchat.Participant, error) {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return lanchat.Participant{}, fmt.Errorf("chat not available")
	}
	return chat.LanChatSelf()
}

// LanChatSetApprovalPolicy sets the approval policy for a peer by nick.
// policy: "always" (auto-approve), "never" (auto-reject), "" (ask).
func (a *App) LanChatSetApprovalPolicy(peerNick string, policy string) error {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatSetApprovalPolicy(peerNick, policy)
}

// LanChatApprovalPolicies returns all persisted approval policies.
func (a *App) LanChatApprovalPolicies() (map[string]string, error) {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return chat.LanChatApprovalPolicies()
}

// dataURIMIME extracts the declared MIME type from a data URI meta section
// (the part between "data:" and ","), e.g. "image/jpeg;base64" →
// "image/jpeg". Returns "" when no MIME is declared ("data:,..." or
// "data:;base64,..." payloads).
func dataURIMIME(meta string) string {
	if i := strings.IndexByte(meta, ';'); i >= 0 {
		meta = meta[:i]
	}
	if meta == "" || !strings.Contains(meta, "/") {
		return "" // not a MIME type (e.g. "base64" alone)
	}
	return meta
}

func (a *App) SendMessageWithImages(userMsg string, images []PastedImage) error {
	// Snapshot + error convention: see SendMessage (#210).
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not available")
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
			data := strings.TrimSpace(img.Data)
			// #426: when the data is a data URI, its declared MIME and
			// ;base64 marker are authoritative — the caller-provided MimeType
			// may be empty (defaulting every image to image/png, mislabeling
			// JPEGs) and non-base64 (URL-encoded) URIs must not be shipped
			// as if they were base64 bytes.
			if strings.HasPrefix(data, "data:") {
				if idx := strings.Index(data, ","); idx >= 0 {
					meta := data[5:idx] // between "data:" and ","
					if uriMime := dataURIMIME(meta); uriMime != "" {
						mime = uriMime // data URI MIME overrides caller hint
					}
					if !strings.Contains(meta, ";base64") && !strings.HasPrefix(meta, "base64") {
						// URL-encoded (percent-encoded) payload, not base64.
						raw, _ := url.QueryUnescape(data[idx+1:])
						if raw == "" {
							continue
						}
						data = base64.StdEncoding.EncodeToString([]byte(raw))
					} else {
						data = data[idx+1:]
					}
				}
			}
			if mime == "" {
				mime = "image/png"
			}
			if data == "" {
				continue
			}
			content = append(content, provider.ImageBlock(mime, data))
		}
		if len(content) == 0 {
			// Silent returns make image-only sends vanish without feedback —
			// emit the same error event the SendContent failure path uses
			// so the user can tell the image never reached the agent (#211).
			raw, _ := json.Marshal(map[string]string{"message": "image data was empty; nothing was sent"})
			a.emitStreamEvent("error", raw)
			return
		}
		if err := chat.SendContent(content); err != nil {
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
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return nil
	}
	return chat.GetModelInfo()
}

func (a *App) CycleReasoningEffort() (map[string]interface{}, error) {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return map[string]interface{}{"effort": "auto", "supported": false}, nil
	}
	effort, supported := chat.CycleReasoningEffort()
	return map[string]interface{}{"effort": effort, "supported": supported}, nil
}

func (a *App) GetTeamBoard() []swarm.TeamBoardSnapshot {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return []swarm.TeamBoardSnapshot{}
	}
	return chat.GetTeamBoard()
}

// IsWorking reports whether the agent loop is currently running.
func (a *App) IsWorking() bool {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return false
	}
	return chat.IsWorking()
}

// SetPermissionMode changes the agent permission mode at wailsruntime.
func (a *App) SetPermissionMode(mode string) {
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		chat.SetPermissionMode(mode)
	}
}

// --- Desktop Notification API (called from frontend) ---

// SetWindowFocused tells the backend whether the window currently has focus.
// Notifications are suppressed when focused, and the unread badge resets.
func (a *App) SetWindowFocused(focused bool) {
	if a.notifications != nil {
		a.notifications.SetFocused(focused)
	}
	if focused && a.lastCloseAttempt != nil {
		// The window is visible and focused again (e.g. restored via Dock
		// icon click, which Wails handles natively without notifying Go).
		// Clear the close-to-tray flag so the global hotkey toggle reflects
		// actual visibility instead of a stale hidden-state heuristic (#158).
		a.lastCloseAttempt = nil
	}
}

// SetNotificationsEnabled toggles the master notification switch and persists to config.
func (a *App) SetNotificationsEnabled(enabled bool) error {
	if a.notifications != nil {
		a.notifications.SetEnabled(enabled)
	}
	if a.dc != nil {
		a.dc.SetNotificationsEnabled(enabled)
		if err := a.dc.Save(); err != nil {
			debug.Log("desktop", "persist notifications-enabled failed: %v", err)
			return fmt.Errorf("persist notification setting: %w", err)
		}
	}
	return nil
}

// GetUnreadNotifications returns the current unread notification count.
func (a *App) GetUnreadNotifications() int {
	if a.notifications == nil {
		return 0
	}
	return a.notifications.GetUnread()
}

// GetFontZoom returns the persisted font zoom level (default 1.0 = 100%).
func (a *App) GetFontZoom() float64 {
	if a.dc != nil {
		return a.dc.GetFontZoom()
	}
	return 1.0
}

// SetFontZoom persists the font zoom level and returns the clamped value.
func (a *App) SetFontZoom(zoom float64) (float64, error) {
	if zoom < 0.7 {
		zoom = 0.7
	}
	if zoom > 1.8 {
		zoom = 1.8
	}
	if a.dc != nil {
		a.dc.SetFontZoom(zoom)
		if err := a.dc.Save(); err != nil {
			debug.Log("desktop", "persist font-zoom failed: %v", err)
			return zoom, fmt.Errorf("persist font zoom: %w", err)
		}
	}
	return zoom, nil
}

// SwitchModel changes the active model at wailsruntime.
func (a *App) SwitchModel(model string) error {
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		return chat.SwitchModel(model)
	}
	return fmt.Errorf("chat not initialized")
}

// GetAvailableModels returns models available for current endpoint.
func (a *App) GetAvailableModels() []string {
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		return chat.GetAvailableModels()
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

// GetHooks returns the current hooks configuration.
func (a *App) GetHooks() (wailskit.HookConfigJSON, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.GetHooks(), nil
	}
	return wailskit.HookConfigJSON{}, nil
}

// SaveHooks saves the hooks configuration.
func (a *App) SaveHooks(cfg wailskit.HookConfigJSON) error {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.SaveHooks(cfg)
	}
	return fmt.Errorf("chat bridge not available")
}

// TestHookMatch tests a hook match pattern against a tool name and raw input.
func (a *App) TestHookMatch(mode, pattern, toolName, rawInput string) wailskit.TestHookMatchResult {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.TestHookMatch(mode, pattern, toolName, rawInput)
	}
	return wailskit.TestHookMatchResult{Error: "chat bridge not available"}
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

// SetEndpointLimits updates context_window and max_tokens for a vendor/endpoint.
// A value of 0 means "auto" (clears the override).
func (a *App) SetEndpointLimits(vendor, endpoint string, contextWindow, maxTokens int) error {
	if err := wailskit.SetEndpointLimits(vendor, endpoint, contextWindow, maxTokens); err != nil {
		return err
	}
	// Refresh the running agent's ContextManager so changes take effect
	// immediately without requiring a session restart.
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		chat.RefreshEndpointLimits()
	}
	return nil
}

// SetModelLimits updates per-model context_window and max_tokens overrides
// for a vendor/endpoint/model combination. A value of 0 means "auto" (clears
// the override, falling back to endpoint-level or inference).
func (a *App) SetModelLimits(vendor, endpoint, model string, contextWindow, maxTokens int) error {
	if err := wailskit.SetModelLimits(vendor, endpoint, model, contextWindow, maxTokens); err != nil {
		return err
	}
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		chat.RefreshEndpointLimits()
	}
	return nil
}

// GetModelLimits returns all per-model limit overrides for the active endpoint.
func (a *App) GetModelLimits() []wailskit.ModelLimitInfo {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return nil
	}
	return chat.GetModelLimits()
}

// GetSessionLimits returns the current session's context_window and max_tokens.
func (a *App) GetSessionLimits() wailskit.SessionLimitInfo {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return wailskit.SessionLimitInfo{}
	}
	return chat.GetSessionLimits()
}

// SetSessionLimits updates the current session's context_window and max_tokens.
// A value of 0 means "auto" (falls back to endpoint/per-model config).
func (a *App) SetSessionLimits(contextWindow, maxTokens int) error {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return fmt.Errorf("chat bridge not initialized")
	}
	return chat.SetSessionLimits(contextWindow, maxTokens)
}

// GetAnthropicOAuthStatus returns whether the user is logged in via Anthropic OAuth.
func (a *App) GetAnthropicOAuthStatus() bool {
	return wailskit.AnthropicOAuthStatus()
}

// StartAnthropicOAuth initiates the OAuth flow, opens the browser, and returns the URL.
func (a *App) StartAnthropicOAuth() (string, error) {
	url, err := wailskit.StartAnthropicOAuth()
	if err != nil {
		return "", err
	}
	if a.ctx != nil && url != "" {
		wailsruntime.BrowserOpenURL(a.ctx, url)
	}
	return url, nil
}

// CompleteAnthropicOAuth blocks until the OAuth callback is received and saves the token.
// Should be called from a goroutine after StartAnthropicOAuth.
func (a *App) CompleteAnthropicOAuth() error {
	return wailskit.CompleteAnthropicOAuth()
}

// LogoutAnthropicOAuth removes the stored Anthropic OAuth token.
func (a *App) LogoutAnthropicOAuth() error {
	return wailskit.LogoutAnthropicOAuth()
}

// ─── Sessions ─────────────────────────────────────────────

// ListSessions returns sessions for the current workspace.
func (a *App) ListSessions() ([]wailskit.SessionInfo, error) {
	return wailskit.ListSessions(a.workDir, a.chat)
}

// GetCurrentSessionID returns the ID of the currently active session,
// or empty string if no session is loaded. Called by the frontend on mount
// to sync state (the session:changed event may fire before the listener
// is registered).
func (a *App) GetCurrentSessionID() (string, error) {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return "", nil
	}
	return chat.CurrentSessionID(), nil
}

// DeleteSession removes a session by ID.
func (a *App) DeleteSession(id string) error {
	// Maintain the session-switch cleanup invariant that NewSession and
	// LoadSession already follow: deleting the CURRENT session must also
	// cancel the running agent and stop tunnel sharing, or later messages
	// resurrect the deleted session (#209).
	chat := a.chat // #457: single-read snapshot
	if chat != nil && chat.CurrentSessionID() == id {
		chat.Cancel()
		a.stopShareForSessionChange()
		// #297: Cancel alone keeps currentSes/sessionLock alive, so the next
		// SendContent reuses the deleted session and O_CREATE-resurrects the
		// JSONL. ClearCurrentSession releases the lock and nils the pointer,
		// forcing ensureSession to start a fresh session.
		chat.ClearCurrentSession()
	}
	// #305: tombstone the ID before the on-disk delete — the run goroutine
	// cancelled above may still be draining and its late persists must not
	// O_CREATE-resurrect the deleted session.
	chat.MarkSessionDeleted(id)
	return wailskit.DeleteSession(id)
}

// RenameSession updates the title of a session by ID.
func (a *App) RenameSession(id string, title string) error {
	return wailskit.RenameSession(id, title)
}

// NewSession creates a fresh initialized session, cancelling any current work.
func (a *App) NewSession() (string, error) {
	chat := a.chat // #457: single-read snapshot
	if a.chat == nil {
		return "", nil
	}
	a.chat.Cancel()
	a.stopShareForSessionChange()
	return chat.StartNewSession()
}

// resumeLatestSession loads the most recent session for the current workspace.
// Returns the session ID if successful, empty string if no sessions exist.
func (a *App) resumeLatestSession() string {
	chat := a.chat
	if chat == nil {
		return ""
	}
	wd := chat.WorkingDir()
	if wd == "" {
		return ""
	}
	store, err := session.NewJSONLStore(filepath.Join(config.HomeDir(), ".ggcode", "sessions"))
	if err != nil {
		debug.Log("app", "resumeLatestSession: failed to open session store: %v", err)
		return ""
	}

	// Try the latest session first (fast path).
	latest, err := store.LatestForWorkspace(wd)
	if err == nil && latest != nil {
		if loadErr := chat.LoadSession(latest.ID); loadErr == nil {
			debug.Log("app", "resumed latest session: %s", latest.ID)
			return latest.ID
		}
		// Latest is locked or load failed — fall through to iterate.
		debug.Log("app", "resumeLatestSession: latest session %s unavailable, trying others", latest.ID)
	}

	// Fall back: iterate all sessions for this workspace, try each until
	// one loads successfully (i.e. is not locked by another instance).
	sessions, err := store.ListForWorkspace(wd)
	if err != nil || len(sessions) == 0 {
		return ""
	}
	for _, ses := range sessions {
		// Skip the one we already tried.
		if latest != nil && ses.ID == latest.ID {
			continue
		}
		if err := chat.LoadSession(ses.ID); err == nil {
			debug.Log("app", "resumed session: %s (latest was locked)", ses.ID)
			return ses.ID
		}
	}
	debug.Log("app", "resumeLatestSession: no unlocked sessions found for %s", wd)
	return ""
}

// LoadSession loads an existing session by ID.
func (a *App) LoadSession(id string) error {
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		chat.Cancel()
		a.stopShareForSessionChange()
		return chat.LoadSession(id)
	}
	return fmt.Errorf("chat not initialized")
}

// GetSessionHistory returns messages from the current session.
func (a *App) GetSessionHistory() ([]wailskit.SessionMessage, error) {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return nil, nil
	}
	return chat.CurrentSessionHistory(), nil
}

// ExportSessionAsMarkdown exports a session to Markdown text.
// If sessionID is empty, exports the current session.
func (a *App) ExportSessionAsMarkdown(sessionID string) (string, error) {
	return wailskit.ExportSessionToMarkdown(sessionID)
}

// ExportSessionAsJSON exports a session to JSON text.
// If sessionID is empty, exports the current session.
func (a *App) ExportSessionAsJSON(sessionID string) (string, error) {
	return wailskit.ExportSessionToJSON(sessionID)
}

// SaveExportedFile shows a native save dialog and writes content to the chosen path.
func (a *App) SaveExportedFile(defaultName string, content string) (string, error) {
	path, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "Export Session",
		DefaultFilename: defaultName,
	})
	if err != nil {
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if path == "" {
		return "", nil // user cancelled
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return path, nil
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

func (a *App) SaveA2AEnabled(enabled bool) error {
	return wailskit.SaveA2AEnabled(enabled)
}

// SelectDirectory opens a native directory picker.
func (a *App) SelectDirectory() (string, error) {
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
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
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return nil, fmt.Errorf("chat not initialized")
	}
	return chat.StartMCPOAuth(a.ctx, name, func(url string) error {
		wailsruntime.BrowserOpenURL(a.ctx, url)
		return nil
	})
}

func (a *App) CompleteMCPOAuth(name string) error {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return fmt.Errorf("chat not initialized")
	}
	return chat.CompleteMCPOAuth(a.ctx, name)
}

// AddMCPServer adds a new MCP server.
func (a *App) AddMCPServer(values map[string]string) error {
	return wailskit.AddMCPServer(values)
}

// RemoveMCPServer removes an MCP server.
func (a *App) RemoveMCPServer(name string) error {
	return wailskit.RemoveMCPServer(name)
}

// ─── Cron Jobs ───────────────────────────────────────────

// ListCronJobs returns all cron jobs for the current session.
func (a *App) ListCronJobs() []wailskit.CronJobInfo {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.ListCronJobs()
	}
	return nil
}

// GetCronJob returns a single cron job by ID.
func (a *App) GetCronJob(id string) (wailskit.CronJobInfo, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.GetCronJob(id)
	}
	return wailskit.CronJobInfo{}, fmt.Errorf("chat bridge not available")
}

// CreateCronJob creates a new cron job.
func (a *App) CreateCronJob(cronExpr, prompt string, recurring, queueIfBusy bool) (wailskit.CronJobInfo, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.CreateCronJob(cronExpr, prompt, recurring, queueIfBusy)
	}
	return wailskit.CronJobInfo{}, fmt.Errorf("chat bridge not available")
}

// UpdateCronJob updates an existing cron job.
func (a *App) UpdateCronJob(id, cronExpr, prompt string, queueIfBusy bool) (wailskit.CronJobInfo, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.UpdateCronJob(id, cronExpr, prompt, queueIfBusy)
	}
	return wailskit.CronJobInfo{}, fmt.Errorf("chat bridge not available")
}

// DeleteCronJob removes a cron job by ID.
func (a *App) DeleteCronJob(id string) error {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.DeleteCronJob(id)
	}
	return fmt.Errorf("chat bridge not available")
}

// PauseCronJob suspends a cron job.
func (a *App) PauseCronJob(id string) error {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.PauseCronJob(id)
	}
	return fmt.Errorf("chat bridge not available")
}

// ResumeCronJob reactivates a paused cron job.
func (a *App) ResumeCronJob(id string) error {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.ResumeCronJob(id)
	}
	return fmt.Errorf("chat bridge not available")
}

// GenerateCronPrompt uses the current LLM provider to generate a cron prompt from a description.
func (a *App) GenerateCronPrompt(description string) (string, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.GenerateCronPrompt(description)
	}
	return "", fmt.Errorf("chat bridge not available")
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
	return wailsruntime.Environment(a.ctx).Platform
}

// ToggleAlwaysOnTop toggles the window's always-on-top state.
// When enabled, the window floats above all other application windows.
func (a *App) ToggleAlwaysOnTop() (bool, error) {
	if a.ctx == nil || a.dc == nil {
		return false, fmt.Errorf("app not initialized")
	}
	newState := !a.dc.IsAlwaysOnTop()
	wailsruntime.WindowSetAlwaysOnTop(a.ctx, newState)
	a.dc.SetAlwaysOnTop(newState)
	if err := a.dc.Save(); err != nil {
		debug.Log("desktop", "persist always-on-top failed: %v", err)
		return newState, fmt.Errorf("persist always-on-top: %w", err)
	}
	debug.Log("desktop", "always-on-top toggled to %v", newState)
	return newState, nil
}

// IsAlwaysOnTop returns the current always-on-top state.
func (a *App) IsAlwaysOnTop() bool {
	if a.dc == nil {
		return false
	}
	return a.dc.IsAlwaysOnTop()
}

// SetGlobalHotkeyEnabled toggles the system-wide global hotkey.
// When enabled, Option+Command+G shows/hides the window from any app.
func (a *App) SetGlobalHotkeyEnabled(enabled bool) error {
	if a.dc == nil {
		return fmt.Errorf("app not initialized")
	}
	a.dc.SetGlobalHotkey(enabled)
	if err := a.dc.Save(); err != nil {
		debug.Log("desktop", "persist global-hotkey failed: %v", err)
		return fmt.Errorf("persist global hotkey setting: %w", err)
	}
	if enabled {
		a.initGlobalHotkey()
	} else {
		a.removeGlobalHotkey()
	}
	debug.Log("desktop", "global hotkey set to %v", enabled)
	return nil
}

// IsGlobalHotkeyEnabled returns the current global hotkey state.
func (a *App) IsGlobalHotkeyEnabled() bool {
	if a.dc == nil {
		return false
	}
	return a.dc.IsGlobalHotkeyEnabled()
}

// workspaceRoot returns the containment root for file-browsing APIs. It
// prefers the configured workspace directory (a.workDir) and falls back to
// the process working directory when unset (#329).
func (a *App) workspaceRoot() string {
	if a.workDir != "" {
		return a.workDir
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// ListFiles returns files in the given directory (1 level deep).
// #329: resolves symlinks and enforces containment within the workspace root
// (same policy as wailskit.ListDirectory) before listing.
func (a *App) ListFiles(dir string) []map[string]interface{} {
	if dir == "" {
		dir = a.workspaceRoot()
	}
	abs, err := wailskit.ResolveContainedPath(a.workspaceRoot(), dir)
	if err != nil {
		debug.Log("desktop", "ListFiles denied %q: %v", dir, err)
		return nil
	}
	entries, err := os.ReadDir(abs)
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
// #329: delegates to wailskit.ReadFileContent's policy — symlink resolution,
// workspace-root containment, and the 20MB text preview cap — anchored at the
// app workspace root instead of os.Getwd().
func (a *App) ReadFileContent(path string) (string, error) {
	resolved, err := wailskit.ResolveContainedPath(a.workspaceRoot(), path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.Size() > wailskit.MaxReadFileTextBytes {
		return "", fmt.Errorf("file too large to preview: %s is %d bytes (limit 20MB)", resolved, info.Size())
	}
	data, err := os.ReadFile(resolved)
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

// maxReadFileBase64Bytes caps files returned by ReadFileAsBase64 (FileBrowser
// PDF/media preview). Rationale: Wails bridge memory peak is roughly
// size*1.33 (base64) plus the JS string copy; 150MB (~200MB after base64)
// balances protection against legitimate large media previews.
const maxReadFileBase64Bytes = 150 << 20

// ReadFileAsBase64 reads a binary file (image, PDF, etc.) and returns base64 data.
// #329: resolves symlinks and enforces workspace-root containment (same policy
// as the other file APIs) in addition to the 150MB preview cap.
func (a *App) ReadFileAsBase64(path string) (*FileBinaryData, error) {
	abs, err := wailskit.ResolveContainedPath(a.workspaceRoot(), path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxReadFileBase64Bytes {
		return nil, fmt.Errorf("file is %.1fMB, exceeding the %dMB preview limit; please open it in an external application instead",
			float64(info.Size())/(1<<20), maxReadFileBase64Bytes/(1<<20))
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
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		chat.RespondApproval(requestID, decision)
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
			debug.Log("desktop", "initIMRuntime panic: %v", r)
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
		RegisterInstance: false, // Deferred to bindCurrentIMSession where session ID is available
		OnUpdate: func(snap im.StatusSnapshot) {
			// Pairing code dialog
			if snap.PendingPairing != nil {
				ch := snap.PendingPairing
				wailsruntime.EventsEmit(a.ctx, "im:pairing", map[string]string{
					"adapter": ch.Adapter, "platform": string(ch.Platform), "code": ch.Code, "kind": string(ch.Kind),
				})
			} else {
				// Pairing complete — dismiss dialog
				wailsruntime.EventsEmit(a.ctx, "im:pairing_done", map[string]string{})
			}
			// Push status to frontend via both Wails events and stream events
			raw, _ := json.Marshal(snap)
			chat := a.chat // #457: single-read snapshot
			if chat != nil && chat.OnStreamEvent != nil {
				chat.OnStreamEvent("im:status", raw)
			}
			wailsruntime.EventsEmit(a.ctx, "im:status", map[string]interface{}{
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
		debug.Log("desktop", "im: auto-muted IM channels, another instance is primary")
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
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		lang := ""
		if cfg != nil {
			lang = cfg.Language
		}
		chat.Emitter = im.NewIMEmitter(a.imManager, lang, a.workDir)
		// Wire IM tool to the runtime manager
		chat.SetIMManager(im.NewToolManagerAdapter(a.imManager))
	}
	chat.SetRuntimeStatusProvider()

	a.imManager.SetBridge(&im.InteractiveTextBridge{
		Submit: func(_ context.Context, text string, adapterName string) error {
			if a == nil || a.chat == nil {
				return fmt.Errorf("app not available")
			}
			safego.Run("im-inbound", func() {
				_ = chat.SendNonUIMessage(text, "im", adapterName)
			})
			return nil
		},
		CurrentApproval: func() (string, string, bool) {
			if a == nil || a.chat == nil {
				return "", "", false
			}
			return chat.PendingApprovalRequest()
		},
		ResolveApproval: func(requestID, decision string) {
			if a == nil || a.chat == nil {
				return
			}
			chat.RespondApproval(requestID, decision)
		},
		CurrentAskUser: func() (string, tool.AskUserRequest, bool) {
			if a == nil || a.chat == nil {
				return "", tool.AskUserRequest{}, false
			}
			return chat.PendingAskUserRequest()
		},
		ResolveAskUser: func(requestID string, response tool.AskUserResponse) {
			if a == nil || a.chat == nil {
				return
			}
			chat.RespondAskUser(requestID, response)
		},
	})

	// StartCurrentBindingAdapter is deferred to bindCurrentIMSession()
	// where session ID is available, so only session-owned adapters start.
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
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return wailskit.LSPStatusResponse{}
	}
	return chat.GetLSPStatus()
}

// InstallLSPServer installs a language server for the given language.
func (a *App) InstallLSPServer(languageID, optionID string) wailskit.LSPInstallResult {
	chat := a.chat // #457: single-read snapshot
	if chat == nil {
		return wailskit.LSPInstallResult{Success: false, Output: "chat bridge not initialized"}
	}
	return chat.InstallLSPServer(languageID, optionID)
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
	// Cascade-unbind is handled inside wailskit.RemoveIMAdapter (#299/#396):
	// a ghost binding keyed by adapter name would otherwise be inherited by
	// a same-name rebuild.
	err := wailskit.RemoveIMAdapter(name, a.imManager)
	if err != nil {
		debug.Log("desktop", "IM RemoveAdapter failed: %v", err)
		return err
	}
	return nil
}

// bindCurrentIMSession binds the current session to the IM manager and
// registers this instance for auto-mute detection. This must be called
// AFTER a session is available so that session-scoped binding ownership
// (LastSessionID) works correctly.
func (a *App) bindCurrentIMSession() {
	chat := a.chat // #457: single-read snapshot
	if a.imManager == nil || chat == nil {
		return
	}
	if ses := chat.CurrentSession(); ses != nil {
		// Use the session's workspace, not a.workDir, so cross-workspace
		// session switches correctly rebind IM to the session's workspace.
		imWorkspace := ses.Workspace
		if imWorkspace == "" {
			imWorkspace = a.workDir
		}
		a.imManager.BindSession(im.SessionBinding{
			SessionID: ses.ID,
			Workspace: imWorkspace,
		})
		// Register instance now that session ID is available.
		// This enables session-scoped IM binding ownership: each instance
		// claims/unclaims adapters via LastSessionID instead of all sharing
		// the same workspace-level mutual exclusion.
		if a.imInstanceDetect == nil && a.workDir != "" {
			detect, others, err := a.imManager.RegisterInstance(a.workDir, ses.ID)
			if err != nil {
				debug.Log("desktop", "RegisterInstance error: %v", err)
			} else {
				a.imInstanceDetect = detect
				if len(others) > 0 {
					debug.Log("desktop", "im: registered with session=%s, %d other instance(s) running", ses.ID, len(others))
				}
			}
		}

		// Start adapters for session-owned bindings. This runs after
		// RegisterInstance so session-scoped muting is already applied.
		// StartUnstartedOwnedAdapters only starts non-muted bindings
		// that don't have an active connection yet.
		a.imManager.StartUnstartedOwnedAdapters()
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
		wailsruntime.EventsEmit(a.ctx, "tunnel:disconnected", nil)
		wailsruntime.EventsEmit(a.ctx, "tunnel:session_changed", map[string]string{
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
	chat := a.chat // #457: single-read snapshot
	if a.chat == nil {
		return nil, fmt.Errorf("chat not initialized")
	}
	th := chat.GetTunnelHost()
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
				wailsruntime.EventsEmit(a.ctx, "tunnel:connected", map[string]interface{}{
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
		chat.BindShareCommands(result.Broker, func(language string) {
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
	wailsruntime.EventsEmit(a.ctx, "tunnel:disconnected", nil)
}

func (a *App) stopShare() {
	broker := a.currentTunnelBroker()
	sess := a.currentTunnelSession()
	chat := a.chat // #457: single-read snapshot
	if chat != nil {
		chat.DetachTunnelBroker()
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

	chat := a.chat // #457: single-read snapshot
	if a.chat == nil {
		snapshot.Status = tunnel.StatusData{Status: tunnel.StatusIdle}
		return snapshot
	}
	snapshot.Status = chat.CurrentTunnelStatus()

	// Populate history from agent messages — same as TUI does.
	// Without this, mobile clients receive an empty snapshot for
	// sessions whose projection store is empty.
	msgs := chat.Messages()
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

// SystemAppearance returns the OS-level appearance preference.
// Returns "dark" or "light". Used by the frontend to set initial window
// background before React mounts, preventing a flash of wrong theme.
func (a *App) SystemAppearance() string {
	if isSystemDark() {
		return "dark"
	}
	return "light"
}

// isSystemDark detects whether the OS is in dark mode.
// macOS: uses NSUserDefaults via CGO.
// Linux/Windows: falls back to "dark" (our default UI is dark-themed).
func isSystemDark() bool {
	return detectMacDarkMode()
}
