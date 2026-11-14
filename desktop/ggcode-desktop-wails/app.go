package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/topcheer/ggcode/internal/webui"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx              context.Context
	chat             *wailskit.ChatBridge
	workDir          string
	dc               *wailskit.DesktopConfig
	imManager        *im.Manager
	imController     *im.AdapterController
	imInstanceDetect *im.InstanceDetect

	tunnelMu       sync.RWMutex
	tunnelSession  *tunnel.Session
	tunnelStarting bool

	tunnelStopSeq     int
	tunnelStartStopEq int
	tunnelBroker      *tunnel.Broker

	// #5574: /et (etunnel) public mapping. Naming kept strictly distinct
	// from the P2P share tunnel above (tunnelMu/tunnelSession/tunnelBroker):
	// etSrv lazy-hosts the webui Server on 127.0.0.1:0, etCtl is the tunnel
	// controller (interface so tests can inject a fake).
	etMu         sync.Mutex
	etSrv        *webui.Server
	etCtl        webui.EtunnelController
	etLocalURL   string
	etPollCancel context.CancelFunc

	askUserMu     sync.Mutex
	askUserReq    tool.AskUserRequest
	hasAskUserReq bool

	notifications *NotificationManager

	lastCloseAttempt atomic.Pointer[time.Time]

	hotkeyRegisterHook func() error

	streamEvents chan uiEvent
	streamOnce   sync.Once
	shutdownOnce sync.Once

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

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startEventLoop()

	a.initSystemTray()

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

	a.notifications = NewNotificationManager()
	a.notifications.SetContext(ctx)

	a.dc = wailskit.LoadDesktopConfig()

	if err := a.initGlobalHotkey(); err != nil {
		debug.Log("desktop", "global hotkey registration failed: %v", err)

		if a.dc.IsGlobalHotkeyEnabled() {
			a.dc.SetGlobalHotkey(false)
			if saveErr := a.dc.Save(); saveErr != nil {
				debug.Log("desktop", "hotkey rollback save failed: %v", saveErr)
			}
		}
	}

	a.notifications.SetEnabled(a.dc.IsNotificationsEnabled())

	if a.dc.WindowW > 0 && a.dc.WindowH > 0 {
		wailsruntime.WindowSetSize(ctx, a.dc.WindowW, a.dc.WindowH)
	}

	if a.dc.WindowPosSet || a.dc.WindowX != 0 || a.dc.WindowY != 0 {
		wailsruntime.WindowSetPosition(ctx, a.dc.WindowX, a.dc.WindowY)
	}
	if a.dc.WindowMax {
		wailsruntime.WindowMaximise(ctx)
	}

	if a.dc.IsAlwaysOnTop() {
		wailsruntime.WindowSetAlwaysOnTop(ctx, true)
	}

	if a.dc.WorkDir != "" {
		if info, err := os.Stat(a.dc.WorkDir); err == nil && info.IsDir() {
			a.workDir = a.dc.WorkDir
			_ = os.Chdir(a.workDir)
		} else {

			debug.Log("desktop", "cached workspace %q no longer exists, falling back to home", a.dc.WorkDir)
			a.workDir = config.HomeDir()
			_ = os.Chdir(a.workDir)
			a.dc.WorkDir = ""
		}
	} else {

		a.workDir = config.HomeDir()
		_ = os.Chdir(a.workDir)
	}

	cfg, err := wailskit.LoadConfigForWorkspace(a.workDir)
	if err != nil || cfg == nil || cfg.NeedsOnboard() {
		debug.Log("desktop", "onboarding needed, skipping workspace init")

		if cfg != nil {
			wailskit.SetConfig(cfg)
		}
		return
	}

	if err := a.initWorkspace(a.workDir); err != nil {
		debug.Log("desktop", "initWorkspace failed: %v", err)
	}
}

func (a *App) startEventLoop() {
	a.streamOnce.Do(func() {
		a.streamEvents = make(chan uiEvent, 4096)
		safego.Go("wails-event-loop", func() {
			for ev := range a.streamEvents {

				safego.Run("wails-event-emit", func() {
					if a.ctx == nil {
						return
					}
					wailsruntime.EventsEmit(a.ctx, ev.name, ev.payload)
				})
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

	select {
	case a.streamEvents <- uiEvent{name: name, payload: payload}:
	default:

		if interactiveUIEvent(name) {
			debug.Log("desktop", "enqueueUIEvent: main buffer full, dispatching interactive event %q directly (lossless bypass)", name)
			safego.Run("wails-event-emit", func() {
				if a.ctx == nil {
					return
				}
				wailsruntime.EventsEmit(a.ctx, name, payload)
			})
			return
		}
		debug.Log("desktop", "enqueueUIEvent: dropping UI event %q (buffer full or consumer dead)", name)
	}
}

func interactiveUIEvent(name string) bool {
	switch name {
	case "ask_user:request", "approval:request",
		"ask_user:cancel", "approval:cancel", "pending_consumed":
		return true
	}
	return false
}

func (a *App) initWorkspace(dir string) error {
	if dir == "" {
		return nil
	}
	cfg, err := wailskit.LoadConfigForWorkspace(dir)
	if err != nil {
		cfg = nil
	}
	wailskit.SetConfig(cfg)

	a.dc.SetWorkDir(dir)
	if err := a.dc.Save(); err != nil {
		debug.Log("desktop", "persist workdir failed: %v", err)
	}

	chat, err := wailskit.NewChatBridge()
	if err != nil {

		return err
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

	a.logStream = wailskit.NewLogStream(2000)
	debug.SetLiveSink(func(category, msg string) {
		a.logStream.Write(category, msg)
	})

	a.initIMRuntime()

	if latestID := a.resumeLatestSession(); latestID != "" {
		debug.Log("app", "resumed latest session: %s", latestID)

	} else {
		chat.EnsureSession()
		_ = chat.InitAgent()
	}

	a.startIMAdapters()

	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "workspace:changed", map[string]interface{}{
			"workDir": dir,
		})
		wailsruntime.EventsEmit(a.ctx, "config:updated", nil)
	}
	return nil
}

func (a *App) ToggleLogStream(enabled bool) {
	if a.logStream != nil {
		a.logStream.ToggleLogStream(enabled)
	}
}

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

	a.enqueueUIEvent("chat:stream", map[string]interface{}{
		"type": envelope.Type,
		"data": envelope.Data,
	})

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

	if a.notifications != nil {
		switch eventType {
		case "run_done":

			if streamEventStringField(data, "error") == "" {
				a.notifications.Notify("GGCode", notifyCompleteBody(data))
			}
		case "error":
			a.notifications.Notify("GGCode", notifyErrorBody(data))
		case "approval:request":
			a.notifications.NotifyApprovalNeeded("GGCode", notifyApprovalBody(data))
		case "ask_user:request":
			a.notifications.NotifyApprovalNeeded("GGCode", notifyAskUserBody(data))
		}
	}
}

func streamEventStringField(data json.RawMessage, key string) string {
	if len(data) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func shortNotifyID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func notifyApprovalBody(data json.RawMessage) string {
	tool := streamEventStringField(data, "toolName")
	req := shortNotifyID(streamEventStringField(data, "requestID"))
	switch {
	case tool != "" && req != "":
		return "Approval needed: " + tool + " (#" + req + ")"
	case tool != "":
		return "Approval needed: " + tool
	case req != "":
		return "Approval needed (#" + req + ")"
	}
	return "Approval needed"
}

func notifyAskUserBody(data json.RawMessage) string {
	title := streamEventStringField(data, "title")
	req := shortNotifyID(streamEventStringField(data, "requestID"))
	switch {
	case title != "" && req != "":
		return "Question from agent: " + title + " (#" + req + ")"
	case title != "":
		return "Question from agent: " + title
	case req != "":
		return "Question from agent (#" + req + ")"
	}
	return "Question from agent"
}

func notifyCompleteBody(data json.RawMessage) string {
	if turn := streamEventStringField(data, "turn_id"); turn != "" {
		return "Task completed (" + shortNotifyID(turn) + ")"
	}
	return "Task completed"
}

func notifyErrorBody(data json.RawMessage) string {
	msg := streamEventStringField(data, "message")
	if len(msg) > 80 {
		msg = msg[:80] + "..."
	}
	if msg != "" {
		return "An error occurred: " + msg
	}
	return "An error occurred"
}

func (a *App) teardownChatBridge() {
	a.stopShare()
	a.stopIMAdapters()
	if a.chat != nil {
		a.chat.Cancel()
		a.chat.Close()
		a.chat = nil
		wailskit.SetChatBridge(nil)
	}
}

func (a *App) switchWorkspace(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}

	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("workspace directory does not exist: %s", dir)
	}

	a.teardownChatBridge()

	a.workDir = dir
	_ = os.Chdir(dir)

	return a.initWorkspace(dir)
}

func (a *App) shutdown(_ context.Context) {
	a.shutdownOnce.Do(func() {

		if a.ctx != nil {
			w, h := wailsruntime.WindowGetSize(a.ctx)
			x, y := wailsruntime.WindowGetPosition(a.ctx)
			maximized := wailsruntime.WindowIsMaximised(a.ctx)
			a.dc.SetWindowState(w, h, x, y, maximized)
			if err := a.dc.Save(); err != nil {
				debug.Log("desktop", "persist window-state on shutdown failed: %v", err)
			}
		}
		a.stopEtunnel()
		a.stopShare()
		a.stopIMAdapters()
		a.removeGlobalHotkey()
		a.removeSystemTray()
		if a.chat != nil {
			a.chat.Cancel()

			a.chat.Close()
		}
	})
}

func (a *App) NeedsOnboard() bool {
	return wailskit.NeedsOnboard()
}

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

func (a *App) InitWorkspace(dir string) error {
	return a.switchWorkspace(dir)
}

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

	a.teardownChatBridge()
	return a.initWorkspace(a.workDir)
}

func (a *App) TrajConfidenceOverview() wailskit.TrajConfidenceOverview {
	return a.chat.TrajConfidenceOverview()
}

func (a *App) GetVendorPresets() []wailskit.VendorPresetInfo {
	return wailskit.GetVendorPresets()
}

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

const maxClipboardImagePixels int64 = 8 * 1000 * 1000

func (a *App) ReadClipboardImage() (*PastedImage, error) {
	img, err := imgpkg.ReadClipboard()
	if err != nil {
		if errors.Is(err, imgpkg.ErrClipboardImageUnavailable) {
			return nil, nil
		}
		return nil, err
	}
	img = imgpkg.DownscaleByPixels(img, maxClipboardImagePixels)
	return &PastedImage{
		MimeType: img.MIME,
		Data:     imgpkg.EncodeBase64(img),
		Name:     "clipboard-image",
	}, nil
}

const (
	maxClipboardAttachmentCount        = 5
	maxClipboardAttachmentTotalBytes   = 30 << 20
	clipboardAttachmentBudgetErrorText = "skipped: clipboard attachment budget exceeded (max %d files, %d MB total)"
)

func (a *App) ReadClipboardAttachments() ([]ClipboardAttachment, error) {
	paths, err := clipboardFilePaths()
	if err != nil {
		return nil, err
	}
	return readClipboardAttachmentsFromPaths(paths), nil
}

func readClipboardAttachmentsFromPaths(paths []string) []ClipboardAttachment {
	attachments := make([]ClipboardAttachment, 0, len(paths))
	seen := map[string]struct{}{}
	totalBytes := 0
	budgetHit := false
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
		if budgetHit || len(seen) > maxClipboardAttachmentCount {
			budgetHit = true
			attachments = append(attachments, ClipboardAttachment{Path: abs, Name: filepath.Base(abs), Kind: "binary", Error: fmt.Sprintf(clipboardAttachmentBudgetErrorText, maxClipboardAttachmentCount, maxClipboardAttachmentTotalBytes>>20)})
			continue
		}
		att := readClipboardFileAttachment(abs)

		if totalBytes+int(att.Size) > maxClipboardAttachmentTotalBytes {
			budgetHit = true
			att.Error = fmt.Sprintf(clipboardAttachmentBudgetErrorText, maxClipboardAttachmentCount, maxClipboardAttachmentTotalBytes>>20)
			att.Data = ""
		} else {

			totalBytes += int(att.Size)
		}
		attachments = append(attachments, att)
	}
	return attachments
}

func clipboardFilePaths() ([]string, error) {
	if runtime.GOOS != "darwin" {

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

		return nil, fmt.Errorf("reading clipboard file paths via osascript: %w", err)
	}
	return parseClipboardPathOutput(string(output)), nil
}

func parseClipboardPathOutput(output string) []string {
	var paths []string

	for _, item := range strings.Split(strings.ReplaceAll(output, "\r", "\n"), "\n") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
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

		if int64(len(img.Data)) > maxClipboardFileBytes {
			att.Error = "File is larger than 10MB"
			return att
		}
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

	if int64(len(data)) > maxClipboardFileBytes {
		att.Error = "File is larger than 10MB"
		return att
	}

	extMime := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	sniffed := http.DetectContentType(data)
	if looksLikeText(data) {
		att.Kind = "text"

		if extMime != "" && isTextualExtensionMime(extMime) {
			att.MimeType = extMime
		} else {
			att.MimeType = sniffed
		}
		att.Content = string(data)
		return att
	}

	att.MimeType = sniffed
	if att.MimeType == "" || strings.HasPrefix(att.MimeType, "text/") {
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

func isTextualExtensionMime(m string) bool {
	if strings.HasPrefix(m, "text/") || strings.HasSuffix(m, "+xml") {
		return true
	}
	switch m {
	case "application/xml", "application/json", "application/javascript",
		"application/x-javascript", "application/yaml", "application/x-yaml",
		"application/toml":
		return true
	}
	return false
}

func (a *App) SendMessage(userMsg string) error {

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

func (a *App) LanChatParticipants() ([]lanchat.Participant, error) {
	chat := a.chat
	if chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return chat.LanChatParticipants()
}

func (a *App) LanChatMessages() ([]lanchat.Message, error) {
	chat := a.chat
	if chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return chat.LanChatMessages()
}

func (a *App) LanChatSend(content, toNodeID, toRole string, asAgent bool) error {
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatSend(content, toNodeID, toRole, asAgent)
}

func (a *App) LanChatSetNick(nick string) error {
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatSetNick(nick)
}

func (a *App) LanChatPendingApprovals() ([]lanchat.PendingAgentMsg, error) {
	chat := a.chat
	if chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return chat.LanChatPendingApprovals()
}

func (a *App) LanChatApprove(messageID string) error {
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatApprove(messageID)
}

func (a *App) LanChatReject(messageID, reason string) error {
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatReject(messageID, reason)
}

func (a *App) LanChatSelf() (lanchat.Participant, error) {
	chat := a.chat
	if chat == nil {
		return lanchat.Participant{}, fmt.Errorf("chat not available")
	}
	return chat.LanChatSelf()
}

func (a *App) LanChatSetApprovalPolicy(peerNick string, policy string) error {
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	return chat.LanChatSetApprovalPolicy(peerNick, policy)
}

func (a *App) LanChatApprovalPolicies() (map[string]string, error) {
	chat := a.chat
	if chat == nil {
		return nil, fmt.Errorf("chat not available")
	}
	return chat.LanChatApprovalPolicies()
}

func dataURIMIME(meta string) string {
	if i := strings.IndexByte(meta, ';'); i >= 0 {
		meta = meta[:i]
	}
	if meta == "" || !strings.Contains(meta, "/") {
		return ""
	}
	return meta
}

func (a *App) SendMessageWithImages(userMsg string, images []PastedImage) error {

	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not available")
	}
	text := strings.TrimSpace(userMsg)
	imgs := append([]PastedImage(nil), images...)

	const maxPasteImages = 5
	const maxPasteTotalBytes = 30 << 20
	if len(imgs) > maxPasteImages {
		return fmt.Errorf("too many pasted images: %d (max %d) - send them in smaller batches", len(imgs), maxPasteImages)
	}
	var total int
	for _, img := range imgs {
		decoded, err := base64.StdEncoding.DecodeString(img.Data)
		if err == nil {
			total += len(decoded)
		} else {

			total += len(img.Data)
		}
	}
	if total > maxPasteTotalBytes {
		return fmt.Errorf("pasted images total %d bytes exceeds the %d byte budget - compress or send in batches", total, maxPasteTotalBytes)
	}
	safego.Go("wails-send-message-images", func() {
		content := make([]provider.ContentBlock, 0, 1+len(imgs))
		if text != "" {
			content = append(content, provider.TextBlock(text))
		}
		for _, img := range imgs {
			mime := strings.TrimSpace(img.MimeType)
			data := strings.TrimSpace(img.Data)

			if strings.HasPrefix(data, "data:") {
				if idx := strings.Index(data, ","); idx >= 0 {
					meta := data[5:idx]
					if uriMime := dataURIMIME(meta); uriMime != "" {
						mime = uriMime
					}
					if !strings.Contains(meta, ";base64") && !strings.HasPrefix(meta, "base64") {

						raw, _ := url.PathUnescape(data[idx+1:])
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

func (a *App) CancelMessage() {
	if a.chat != nil {
		a.chat.Cancel()
	}
}

func (a *App) GetModelInfo() map[string]interface{} {
	chat := a.chat
	if chat == nil {
		return nil
	}
	return chat.GetModelInfo()
}

func (a *App) CycleReasoningEffort() (map[string]interface{}, error) {
	chat := a.chat
	if chat == nil {
		return map[string]interface{}{"effort": "auto", "supported": false}, nil
	}
	effort, supported := chat.CycleReasoningEffort()
	return map[string]interface{}{"effort": effort, "supported": supported}, nil
}

func (a *App) GetTeamBoard() []swarm.TeamBoardSnapshot {
	chat := a.chat
	if chat == nil {
		return []swarm.TeamBoardSnapshot{}
	}
	return chat.GetTeamBoard()
}

func (a *App) IsWorking() bool {
	chat := a.chat
	if chat == nil {
		return false
	}
	return chat.IsWorking()
}

func (a *App) SetPermissionMode(mode string) {
	chat := a.chat
	if chat != nil {
		chat.SetPermissionMode(mode)
	}
}

func (a *App) SetWindowFocused(focused bool) {
	if a.notifications != nil {
		a.notifications.SetFocused(focused)
	}
	if focused && a.lastCloseAttempt.Load() != nil {

		a.lastCloseAttempt.Store(nil)
	}
}

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

func (a *App) GetUnreadNotifications() int {
	if a.notifications == nil {
		return 0
	}
	return a.notifications.GetUnread()
}

func (a *App) GetFontZoom() float64 {
	if a.dc != nil {
		return a.dc.GetFontZoom()
	}
	return 1.0
}

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

func (a *App) SwitchModel(model string) error {
	chat := a.chat
	if chat != nil {
		return chat.SwitchModel(model)
	}
	return fmt.Errorf("chat not initialized")
}

func (a *App) GetAvailableModels() []string {
	chat := a.chat
	if chat != nil {
		return chat.GetAvailableModels()
	}
	return nil
}

func (a *App) GetConfig() (*wailskit.FullConfig, error) {
	return wailskit.GetFullConfig()
}

func (a *App) UpdateConfig(values map[string]interface{}) error {
	if err := wailskit.UpdateConfig(values); err != nil {
		return err
	}

	if bridge := wailskit.GetChatBridge(); bridge != nil {
		bridge.OnConfigProviderChanged()
	}
	return nil
}

func (a *App) GetHooks() (wailskit.HookConfigJSON, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.GetHooks(), nil
	}
	return wailskit.HookConfigJSON{}, nil
}

func (a *App) SaveHooks(cfg wailskit.HookConfigJSON) error {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.SaveHooks(cfg)
	}
	return fmt.Errorf("chat bridge not available")
}

func (a *App) TestHookMatch(mode, pattern, toolName, rawInput string) wailskit.TestHookMatchResult {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.TestHookMatch(mode, pattern, toolName, rawInput)
	}
	return wailskit.TestHookMatchResult{Error: "chat bridge not available"}
}

func (a *App) GetVendors() []string {
	return wailskit.VendorNames()
}

func (a *App) GetEndpoints(vendor string) []wailskit.EndpointInfo {
	return wailskit.EndpointsForVendor(vendor)
}

func (a *App) GetModels(vendor, endpoint string) []string {
	return wailskit.ModelsForEndpoint(vendor, endpoint)
}

func (a *App) SaveAPIKey(vendor, endpoint, apiKey string) error {
	return wailskit.SaveAPIKey(vendor, endpoint, apiKey)
}

func (a *App) GetResolvedEndpoint() (*wailskit.ResolvedEndpointInfo, error) {
	return wailskit.GetResolvedEndpoint()
}

func (a *App) FetchModels(vendor, endpoint, apiKey, baseURL string) ([]string, error) {
	return wailskit.FetchModelsForEndpoint(vendor, endpoint, apiKey, baseURL)
}

func (a *App) GetEndpointDetails(vendor, endpoint string) *wailskit.EndpointDetails {
	return wailskit.GetEndpointDetails(vendor, endpoint)
}

func (a *App) GetImpersonationPresets() []wailskit.ImpersonationPresetInfo {
	return wailskit.GetImpersonationPresets()
}

func (a *App) ApplyImpersonation(presetID, version string, customHeaders map[string]string) error {
	return wailskit.ApplyImpersonation(presetID, version, customHeaders)
}

func (a *App) TestEndpointConnection(protocol, baseURL, apiKey string) (*wailskit.TestEndpointResult, error) {
	return wailskit.TestEndpointConnection(protocol, baseURL, apiKey)
}

func (a *App) AddCustomEndpoint(vendor, name, protocol, baseURL, apiKey string) error {
	return wailskit.AddCustomEndpoint(vendor, name, protocol, baseURL, apiKey)
}

func (a *App) SetEndpointLimits(vendor, endpoint string, contextWindow, maxTokens int) error {
	if err := wailskit.SetEndpointLimits(vendor, endpoint, contextWindow, maxTokens); err != nil {
		return err
	}

	chat := a.chat
	if chat != nil {
		chat.RefreshEndpointLimits()
	}
	return nil
}

func (a *App) SetModelLimits(vendor, endpoint, model string, contextWindow, maxTokens int) error {
	if err := wailskit.SetModelLimits(vendor, endpoint, model, contextWindow, maxTokens); err != nil {
		return err
	}
	chat := a.chat
	if chat != nil {
		chat.RefreshEndpointLimits()
	}
	return nil
}

func (a *App) GetModelLimits() []wailskit.ModelLimitInfo {
	chat := a.chat
	if chat == nil {
		return nil
	}
	return chat.GetModelLimits()
}

func (a *App) GetSessionLimits() wailskit.SessionLimitInfo {
	chat := a.chat
	if chat == nil {
		return wailskit.SessionLimitInfo{}
	}
	return chat.GetSessionLimits()
}

func (a *App) SetSessionLimits(contextWindow, maxTokens int) error {
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat bridge not initialized")
	}
	return chat.SetSessionLimits(contextWindow, maxTokens)
}

func (a *App) GetAnthropicOAuthStatus() bool {
	return wailskit.AnthropicOAuthStatus()
}

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

func (a *App) CompleteAnthropicOAuth() error {
	return wailskit.CompleteAnthropicOAuth()
}

func (a *App) LogoutAnthropicOAuth() error {
	return wailskit.LogoutAnthropicOAuth()
}

func (a *App) ListSessions() ([]wailskit.SessionInfo, error) {
	return wailskit.ListSessions(a.workDir, a.chat)
}

func (a *App) GetCurrentSessionID() (string, error) {
	chat := a.chat
	if chat == nil {
		return "", nil
	}
	return chat.CurrentSessionID(), nil
}

func (a *App) DeleteSession(id string) error {

	chat := a.chat
	if chat != nil && chat.CurrentSessionID() == id {
		chat.Cancel()
		a.stopShareForSessionChange()

		chat.ClearCurrentSession()

		a.enqueueUIEvent("session:changed", map[string]string{"sessionId": ""})
	}

	if chat != nil {
		chat.MarkSessionDeleted(id)
	}
	return wailskit.DeleteSession(id)
}

func (a *App) RenameSession(id string, title string) error {
	return wailskit.RenameSession(id, title)
}

func (a *App) NewSession() (string, error) {
	chat := a.chat
	if chat == nil {
		return "", fmt.Errorf("chat not available")
	}
	chat.Cancel()
	a.stopShareForSessionChange()
	return chat.StartNewSession()
}

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

	latest, err := store.LatestForWorkspace(wd)
	if err == nil && latest != nil {

		loadErr := chat.LoadSession(latest.ID)
		if loadErr == nil {
			debug.Log("app", "resumed latest session: %s", latest.ID)
			return latest.ID
		}

		debug.Log("app", "resumeLatestSession: latest session %s failed to load: %v - trying older sessions", latest.ID, loadErr)
	}

	sessions, err := store.ListForWorkspace(wd)
	if err != nil || len(sessions) == 0 {
		return ""
	}
	for _, ses := range sessions {

		if latest != nil && ses.ID == latest.ID {
			continue
		}
		if err := chat.LoadSession(ses.ID); err == nil {
			debug.Log("app", "resumed older session %s (latest failed to load - see reason above)", ses.ID)
			return ses.ID
		} else {
			debug.Log("app", "resumeLatestSession: session %s also failed: %v", ses.ID, err)
		}
	}
	debug.Log("app", "resumeLatestSession: no unlocked sessions found for %s", wd)
	return ""
}

func (a *App) LoadSession(id string) error {
	chat := a.chat
	if chat != nil {
		chat.Cancel()
		a.stopShareForSessionChange()
		return chat.LoadSession(id)
	}
	return fmt.Errorf("chat not initialized")
}

func (a *App) GetSessionHistory() ([]wailskit.SessionMessage, error) {
	chat := a.chat
	if chat == nil {
		return nil, nil
	}
	return chat.CurrentSessionHistory(), nil
}

func (a *App) ExportSessionAsMarkdown(sessionID string) (string, error) {
	return wailskit.ExportSessionToMarkdown(sessionID)
}

func (a *App) ExportSessionAsJSON(sessionID string) (string, error) {
	return wailskit.ExportSessionToJSON(sessionID)
}

func (a *App) SaveExportedFile(defaultName string, content string) (string, error) {
	path, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		Title:           "Export Session",
		DefaultFilename: defaultName,
	})
	if err != nil {
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if path == "" {
		return "", nil
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return path, nil
}

func (a *App) GetWorkDir() string {
	return a.workDir
}

func (a *App) SaveDefaultMode(mode string) error {
	return wailskit.SaveDefaultMode(mode)
}

func (a *App) SaveA2AEnabled(enabled bool) error {
	return wailskit.SaveA2AEnabled(enabled)
}

func (a *App) SelectDirectory() (string, error) {
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select Directory",
	})
}

func (a *App) TestIMConnection(name string) error {
	return wailskit.TestIMConnection(name)
}

func (a *App) ListMCPServers() ([]wailskit.MCPServerInfo, error) {
	return wailskit.ListMCPServers()
}

func (a *App) SetMCPServerEnabled(name string, enabled bool) bool {
	return wailskit.SetMCPServerEnabled(name, enabled)
}

func (a *App) ReconnectMCPServer(name string) bool {
	return wailskit.ReconnectMCPServer(name)
}

func (a *App) ForceReauthMCPServer(name string) bool {
	return wailskit.ForceReauthMCPServer(name)
}

func (a *App) StartMCPOAuth(name string) (*wailskit.MCPOAuthStartResult, error) {
	chat := a.chat
	if chat == nil {
		return nil, fmt.Errorf("chat not initialized")
	}
	return chat.StartMCPOAuth(a.ctx, name, func(url string) error {
		wailsruntime.BrowserOpenURL(a.ctx, url)
		return nil
	})
}

func (a *App) CompleteMCPOAuth(name string) error {
	chat := a.chat
	if chat == nil {
		return fmt.Errorf("chat not initialized")
	}
	return chat.CompleteMCPOAuth(a.ctx, name)
}

func (a *App) AddMCPServer(values map[string]string) error {
	return wailskit.AddMCPServer(values)
}

func (a *App) RemoveMCPServer(name string) error {
	return wailskit.RemoveMCPServer(name)
}

func (a *App) ListCronJobs() ([]wailskit.CronJobInfo, error) {
	bridge := wailskit.GetChatBridge()
	if bridge == nil {
		return nil, fmt.Errorf("chat bridge not available")
	}
	return bridge.ListCronJobs()
}

func (a *App) GetUsageInfo() wailskit.UsageInfoResult {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.GetUsageInfo()
	}
	return wailskit.UsageInfoResult{Windows: []wailskit.UsageWindowInfo{}, Error: "chat bridge not available"}
}

func (a *App) GetCronJob(id string) (wailskit.CronJobInfo, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.GetCronJob(id)
	}
	return wailskit.CronJobInfo{}, fmt.Errorf("chat bridge not available")
}

func (a *App) CreateCronJob(cronExpr, prompt string, recurring, queueIfBusy bool) (wailskit.CronJobInfo, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.CreateCronJob(cronExpr, prompt, recurring, queueIfBusy)
	}
	return wailskit.CronJobInfo{}, fmt.Errorf("chat bridge not available")
}

func (a *App) UpdateCronJob(id, cronExpr, prompt string, queueIfBusy bool) (wailskit.CronJobInfo, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.UpdateCronJob(id, cronExpr, prompt, queueIfBusy)
	}
	return wailskit.CronJobInfo{}, fmt.Errorf("chat bridge not available")
}

func (a *App) DeleteCronJob(id string) error {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.DeleteCronJob(id)
	}
	return fmt.Errorf("chat bridge not available")
}

func (a *App) PauseCronJob(id string) error {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.PauseCronJob(id)
	}
	return fmt.Errorf("chat bridge not available")
}

func (a *App) ResumeCronJob(id string) error {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.ResumeCronJob(id)
	}
	return fmt.Errorf("chat bridge not available")
}

func (a *App) GenerateCronPrompt(description string) (string, error) {
	if bridge := wailskit.GetChatBridge(); bridge != nil {
		return bridge.GenerateCronPrompt(description)
	}
	return "", fmt.Errorf("chat bridge not available")
}

func (a *App) GetVersion() string {
	return version.Version
}

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

func (a *App) GetPlatform() string {
	return wailsruntime.Environment(a.ctx).Platform
}

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

func (a *App) IsAlwaysOnTop() bool {
	if a.dc == nil {
		return false
	}
	return a.dc.IsAlwaysOnTop()
}

func (a *App) SetGlobalHotkeyEnabled(enabled bool) error {
	if a.dc == nil {
		return fmt.Errorf("app not initialized")
	}
	oldEnabled := a.dc.IsGlobalHotkeyEnabled()
	if enabled {

		a.dc.SetGlobalHotkey(true)
		if err := a.initGlobalHotkey(); err != nil {
			a.dc.SetGlobalHotkey(oldEnabled)
			debug.Log("desktop", "global hotkey registration failed, rolling back to %v: %v", oldEnabled, err)
			return fmt.Errorf("global hotkey registration failed: %w", err)
		}
		if err := a.dc.Save(); err != nil {

			a.removeGlobalHotkey()
			a.dc.SetGlobalHotkey(oldEnabled)
			debug.Log("desktop", "persist global-hotkey failed: %v", err)
			return fmt.Errorf("persist global hotkey setting: %w", err)
		}
	} else {
		a.dc.SetGlobalHotkey(false)
		a.removeGlobalHotkey()
		if err := a.dc.Save(); err != nil {

			a.dc.SetGlobalHotkey(oldEnabled)
			if oldEnabled {
				if reRegErr := a.initGlobalHotkey(); reRegErr != nil {
					debug.Log("desktop", "re-registration of previously-enabled global hotkey failed after Save rollback: %v", reRegErr)
				}
			}
			debug.Log("desktop", "persist global-hotkey failed: %v", err)
			return fmt.Errorf("persist global hotkey setting: %w", err)
		}
	}
	debug.Log("desktop", "global hotkey set to %v", enabled)
	return nil
}

func (a *App) IsGlobalHotkeyEnabled() bool {
	if a.dc == nil {
		return false
	}
	return a.dc.IsGlobalHotkeyEnabled()
}

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

type FileBinaryData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

const maxReadFileBase64Bytes = 150 << 20

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

	if int64(len(data)) > maxReadFileBase64Bytes {
		return nil, fmt.Errorf("file data is %.1fMB, exceeding the %dMB preview limit; please open it in an external application instead",
			float64(len(data))/(1<<20), maxReadFileBase64Bytes/(1<<20))
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

func (a *App) RespondApproval(requestID string, decision string) {
	chat := a.chat
	if chat != nil {
		chat.RespondApproval(requestID, decision)
	}
}

func (a *App) RespondAskUser(requestID string, answersJSON string) {
	chat := a.chat
	if chat == nil {
		return
	}

	var payload struct {
		Status  string               `json:"status"`
		Answers []tool.AskUserAnswer `json:"answers"`
	}
	if err := json.Unmarshal([]byte(answersJSON), &payload); err != nil {

		debug.Log("app", "RespondAskUser: invalid answers payload: %v", err)
		if msg, merr := json.Marshal(map[string]string{
			"message": fmt.Sprintf("ask_user response error: invalid payload: %v", err),
		}); merr == nil {
			a.emitStreamEvent("error", msg)
		}
		chat.RespondAskUser(requestID, tool.AskUserResponse{
			Status: tool.AskUserStatusCancelled,
		})
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

	chat.RespondAskUser(requestID, response)
}

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
		RegisterInstance: false,
		OnUpdate: func(snap im.StatusSnapshot) {

			if snap.PendingPairing != nil {
				ch := snap.PendingPairing
				wailsruntime.EventsEmit(a.ctx, "im:pairing", map[string]string{
					"adapter": ch.Adapter, "platform": string(ch.Platform), "code": ch.Code, "kind": string(ch.Kind),
				})
			} else {

				wailsruntime.EventsEmit(a.ctx, "im:pairing_done", map[string]string{})
			}

			raw, _ := json.Marshal(snap)
			chat := a.chat
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

	a.imInstanceDetect = runtimeInit.InstanceDetect
	if len(runtimeInit.OtherInstances) > 0 {
		debug.Log("desktop", "im: auto-muted IM channels, another instance is primary")
	}
}

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

	chat := a.chat
	if chat != nil {
		lang := ""
		if cfg != nil {
			lang = cfg.Language
		}
		chat.Emitter = im.NewIMEmitter(a.imManager, lang, a.workDir)

		chat.SetIMManager(im.NewToolManagerAdapter(a.imManager))

		chat.SetRuntimeStatusProvider()
	}

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
		PendingApprovals: func() []string {
			if a == nil || a.chat == nil {
				return nil
			}
			return chat.PendingApprovalIDs()
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

}

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

func (a *App) ListIMAdapters() ([]wailskit.IMAdapterInfo, error) {
	return wailskit.ListIMAdapters(a.workDir, a.imManager)
}

func (a *App) GetIMPlatformRegistry() []wailskit.IMPlatformMeta {
	return wailskit.GetIMPlatformRegistry()
}

func (a *App) GetLSPStatus() wailskit.LSPStatusResponse {
	chat := a.chat
	if chat == nil {
		return wailskit.LSPStatusResponse{}
	}
	return chat.GetLSPStatus()
}

func (a *App) InstallLSPServer(languageID, optionID string) wailskit.LSPInstallResult {
	chat := a.chat
	if chat == nil {
		return wailskit.LSPInstallResult{Success: false, Output: "chat bridge not initialized"}
	}
	return chat.InstallLSPServer(languageID, optionID)
}

func (a *App) SaveIMAdapter(name string, values map[string]string) error {
	debug.Log("desktop", "IM SaveAdapter: name=%s platform=%s", name, values["platform"])

	a.imStopAdapter(name)
	err := wailskit.SaveIMAdapter(name, values)
	if err != nil {
		debug.Log("desktop", "IM SaveAdapter failed: %v", err)
		return err
	}

	if values["enabled"] != "false" {
		name := name
		safego.Go("desktop.im-start-save", func() { a.imStartAdapter(name) })
	}
	return nil
}

func (a *App) RemoveIMAdapter(name string) error {
	debug.Log("desktop", "IM RemoveAdapter: name=%s", name)
	a.imStopAdapter(name)

	err := wailskit.RemoveIMAdapter(name, a.imManager)
	if err != nil {
		debug.Log("desktop", "IM RemoveAdapter failed: %v", err)
		return err
	}
	return nil
}

func (a *App) bindCurrentIMSession() {
	chat := a.chat
	if a.imManager == nil || chat == nil {
		return
	}
	if ses := chat.CurrentSession(); ses != nil {

		imWorkspace := ses.Workspace
		if imWorkspace == "" {
			imWorkspace = a.workDir
		}
		a.imManager.BindSession(im.SessionBinding{
			SessionID: ses.ID,
			Workspace: imWorkspace,
		})

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

		a.imManager.StartUnstartedOwnedAdapters()
	}
}

func (a *App) imStartAdapter(name string) {
	if a.imManager == nil {
		debug.Log("desktop", "IM start %s: manager not initialized", name)
		return
	}
	cfg, err := wailskit.LoadConfigForWorkspace(a.workDir)
	if err != nil {
		debug.Log("desktop", "IM start %s: config load failed: %v", name, err)
		return
	}
	if cfg == nil {
		debug.Log("desktop", "IM start %s: no config", name)
		return
	}

	a.bindCurrentIMSession()
	if adapterCfg, ok := cfg.IM.Adapters[name]; ok && !adapterCfg.Enabled {

		debug.Log("desktop", "IM start %s: adapter is bound but DISABLED in config - it will not connect until enabled", name)
		a.emitIMAdapterNotice(name, "bound but disabled - enable it to connect")
		return
	}
	debug.Log("desktop", "IM start: starting adapter %s", name)
	if err := im.StartNamedAdapter(context.Background(), cfg.IM, name, a.imManager); err != nil {
		debug.Log("desktop", "IM start %s failed: %v", name, err)

		a.emitIMAdapterNotice(name, fmt.Sprintf("start failed: %v", err))
	} else {
		debug.Log("desktop", "IM start %s: ok", name)
	}
}

func (a *App) emitIMAdapterNotice(name, msg string) {
	a.enqueueUIEvent(fmt.Sprintf("im:adapter:%s", name), map[string]string{"name": name, "message": msg})
}

func (a *App) imStopAdapter(name string) {
	if a.imManager == nil {
		debug.Log("desktop", "IM stop %s: manager not initialized", name)
		return
	}
	debug.Log("desktop", "IM stop: stopping adapter %s", name)
	a.imManager.StopAdapter(name)
	debug.Log("desktop", "IM stop %s: ok", name)
}

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

func (a *App) MuteIMAdapter(name string, muted bool) error {
	debug.Log("desktop", "IM Mute: name=%s muted=%v", name, muted)
	if a.imManager == nil {
		debug.Log("desktop", "IM Mute failed: IM not initialized")
		return fmt.Errorf("IM not initialized")
	}
	if muted {

		if err := a.imManager.MuteBinding(name); err != nil {
			debug.Log("desktop", "IM MuteBinding failed: %v", err)
			return err
		}
		a.imStopAdapter(name)
	} else {

		if err := a.imManager.UnmuteBinding(name); err != nil {
			debug.Log("desktop", "IM UnmuteBinding failed: %v", err)
			return err
		}
		name := name
		safego.Go("desktop.im-start-unmute", func() { a.imStartAdapter(name) })
	}
	return nil
}

func (a *App) BindIMAdapter(name string) error {
	debug.Log("desktop", "IM Bind: name=%s workDir=%s", name, a.workDir)

	a.imStopAdapter(name)
	err := wailskit.BindIMAdapter(name, a.workDir, a.imManager)
	if err != nil {
		debug.Log("desktop", "IM Bind failed: %v", err)
		return err
	}

	safego.Go("desktop.im-start-bind", func() { a.imStartAdapter(name) })
	return nil
}

func (a *App) RebindIMAdapter(name string) error {
	debug.Log("desktop", "IM Rebind: name=%s workDir=%s", name, a.workDir)

	prevWorkspaces := a.imManager.AdapterBindings(name)
	a.imStopAdapter(name)
	err := wailskit.RebindIMAdapter(name, a.workDir, a.imManager)
	if err != nil {
		debug.Log("desktop", "IM Rebind failed: %v - restoring previous binding", err)
		for _, prev := range prevWorkspaces {
			if rerr := wailskit.BindIMAdapter(name, prev, a.imManager); rerr != nil {
				debug.Log("desktop", "IM Rebind rollback bind %s->%s failed: %v", name, prev, rerr)
			}
		}
		name := name
		safego.Go("desktop.im-start-rollback", func() { a.imStartAdapter(name) })
		return err
	}
	safego.Go("desktop.im-start-rebind", func() { a.imStartAdapter(name) })
	return nil
}

func (a *App) UnbindIMAdapter(name string) error {
	debug.Log("desktop", "IM Unbind: name=%s", name)
	a.imStopAdapter(name)
	err := wailskit.UnbindIMAdapter(name, a.imManager)
	if err != nil {
		debug.Log("desktop", "IM Unbind failed: %v", err)
	}
	return err
}

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

func (a *App) setTunnelState(sess *tunnel.Session, broker *tunnel.Broker) bool {
	a.tunnelMu.Lock()
	defer a.tunnelMu.Unlock()

	if a.tunnelStopSeq != a.tunnelStartStopEq {
		return false
	}
	a.tunnelSession = sess
	a.tunnelBroker = broker
	return true
}

func (a *App) clearTunnelState() {
	a.tunnelMu.Lock()
	defer a.tunnelMu.Unlock()
	a.tunnelSession = nil
	a.tunnelBroker = nil
	a.tunnelStarting = false

	a.tunnelStopSeq++
}

func (a *App) tunnelStartStale() bool {
	a.tunnelMu.RLock()
	defer a.tunnelMu.RUnlock()
	return a.tunnelStopSeq != a.tunnelStartStopEq
}

func (a *App) IsSharing() bool {
	a.tunnelMu.RLock()
	sess := a.tunnelSession
	starting := a.tunnelStarting
	a.tunnelMu.RUnlock()
	return sess != nil || starting
}

func (a *App) StartShare() (*ShareInfo, error) {

	a.tunnelMu.Lock()
	switch {
	case a.tunnelSession != nil:

		a.tunnelMu.Unlock()
	case a.tunnelStarting:

		a.tunnelMu.Unlock()
		return nil, fmt.Errorf("share start already in progress")
	default:
		a.tunnelStarting = true

		a.tunnelStartStopEq = a.tunnelStopSeq
		a.tunnelMu.Unlock()
		defer func() {
			a.tunnelMu.Lock()
			a.tunnelStarting = false
			a.tunnelMu.Unlock()
		}()
	}

	if sess := a.currentTunnelSession(); sess != nil {
		info, err := sess.RefreshInvite(context.Background())
		if err != nil {

			debug.Log("share", "refresh invite failed, restarting share: %v", err)
			a.stopShare()

			a.tunnelMu.Lock()
			a.tunnelStartStopEq = a.tunnelStopSeq
			a.tunnelStarting = true
			a.tunnelMu.Unlock()
			defer func() {
				a.tunnelMu.Lock()
				a.tunnelStarting = false
				a.tunnelMu.Unlock()
			}()
		} else {
			return &ShareInfo{
				ConnectURL:   info.ConnectURL,
				QRCodeBase64: encodeQRBase64(info.QRCodePNG),
			}, nil
		}
	}

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

	chat := a.chat

	if chat == nil {
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

	if a.tunnelStartStale() {
		if chat != nil {
			chat.DetachTunnelBroker()
		}
		agentruntime.StopSharedTunnelGracefully(result.Session, result.Broker, 2*time.Second)
		return nil, fmt.Errorf("share was stopped while starting")
	}

	if !a.setTunnelState(result.Session, result.Broker) {
		if chat != nil {
			chat.DetachTunnelBroker()
		}
		agentruntime.StopSharedTunnelGracefully(result.Session, result.Broker, 2*time.Second)
		return nil, fmt.Errorf("share was stopped while starting")
	}

	if result.Broker != nil {
		chat.BindShareCommands(result.Broker, func(language string) {
			c, _ := wailskit.LoadConfigForWorkspace(a.workDir)
			if c != nil {
				_ = c.SaveLanguagePreference(language)
			}
		}, a.currentAskUserRequest, a.clearAskUserRequest)
	}

	return &ShareInfo{
		ConnectURL:   result.ConnectURL,
		QRCodeBase64: encodeQRBase64(result.QRCodePNG),
	}, nil
}

func (a *App) StopShare() {
	a.stopShare()
	wailsruntime.EventsEmit(a.ctx, "tunnel:disconnected", nil)
}

func (a *App) stopShare() {
	broker := a.currentTunnelBroker()
	sess := a.currentTunnelSession()
	chat := a.chat
	if chat != nil {
		chat.DetachTunnelBroker()
	}
	agentruntime.StopSharedTunnelGracefully(sess, broker, 2*time.Second)
	a.clearTunnelState()
}

func (a *App) tunnelSnapshot() tunnel.BrokerSnapshot {
	snapshot := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{
			Workspace: a.workDir,
			Version:   a.GetVersion(),
			Language:  a.dc.Language,
		},
	}

	if cfg, err := wailskit.LoadConfigForWorkspace(a.workDir); err == nil {

		snapshot.SessionInfo.Mode = cfg.DefaultMode
		if resolved, rerr := cfg.ResolveActiveEndpoint(); rerr == nil && resolved != nil {
			snapshot.SessionInfo.Provider = resolved.VendorName
			snapshot.SessionInfo.Model = resolved.Model
		} else {
			snapshot.SessionInfo.Provider = cfg.Vendor
			snapshot.SessionInfo.Model = cfg.Model
		}
	}

	chat := a.chat

	if chat == nil {
		snapshot.Status = tunnel.StatusData{Status: tunnel.StatusIdle}
		return snapshot
	}
	snapshot.Status = chat.CurrentTunnelStatus()

	msgs := chat.Messages()
	if len(msgs) > 0 {
		snapshot.History = messagesToTunnelHistory(msgs)
	}

	return snapshot
}

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

func (a *App) currentAskUserRequest() tool.AskUserRequest {
	a.askUserMu.Lock()
	defer a.askUserMu.Unlock()
	return a.askUserReq
}

func (a *App) clearAskUserRequest() {
	a.askUserMu.Lock()
	defer a.askUserMu.Unlock()
	a.hasAskUserReq = false
	a.askUserReq = tool.AskUserRequest{}
}

func encodeQRBase64(pngData []byte) string {
	if len(pngData) == 0 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData)
}

func (a *App) SystemAppearance() string {
	if isSystemDark() {
		return "dark"
	}
	return "light"
}

func isSystemDark() bool {
	return detectMacDarkMode()
}
