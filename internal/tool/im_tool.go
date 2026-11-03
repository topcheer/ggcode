package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

type IMManager interface {
	Snapshot() IMSnapshot
	MuteBinding(adapterName string) error
	UnmuteBinding(adapterName string) error
	DisableBinding(adapterName string) error
	EnableBinding(adapterName string) error
	IsBindingMuted(adapterName string) bool
	IsBindingDisabled(adapterName string) bool
	Emit(ctx context.Context, event IMOutboundEvent) error
	SendDirect(ctx context.Context, adapter string, event IMOutboundEvent) error
	SendFileDirect(ctx context.Context, adapter string, file IMOutboundFile, caption string) (supported bool, err error)
	OtherInstancesHaveActiveChannels() bool
}

type IMSnapshot struct {
	CurrentBindings  []IMChannelBinding
	DisabledBindings []IMChannelBinding
	Adapters         []IMAdapterState
}

type IMChannelBinding struct {
	Adapter   string
	Platform  string
	ChannelID string
	Muted     bool
}

type IMAdapterState struct {
	Name      string
	Platform  string
	Healthy   bool
	Status    string
	LastError string
}

type IMOutboundEvent struct {
	Kind string
	Text string
}

type IMOutboundFile struct {
	Path     string
	Filename string
	MIME     string
	Data     []byte
}

type IMTool struct {
	Manager IMManager
}

func (t IMTool) Name() string { return "im" }

func (t IMTool) Description() string {

	if t.Manager == nil {
		return "Manage IM adapters and send messages to bound IM channels. " +
			"No IM manager is available in this session (IM not configured or not started yet)."
	}
	snap := t.Manager.Snapshot()
	bound := make([]IMChannelBinding, 0, len(snap.CurrentBindings))
	bound = append(bound, snap.CurrentBindings...)
	if len(snap.DisabledBindings) > 0 {
		bound = append(bound, snap.DisabledBindings...)
	}
	if len(bound) == 0 {
		return "Manage IM adapters and send messages to bound IM channels. " +
			"No IM adapter is bound in this workspace yet. Bind one from the IM panel (or send it a message) before using this tool."
	}

	mediaCapable := map[string]bool{
		"qq": true, "telegram": true, "discord": true, "feishu": true,
		"matrix": true, "whatsapp": true, "slack": true, "mattermost": true,
		"signal": true, "wecom": true, "wechat": true,
	}
	var sb strings.Builder
	sb.WriteString("Manage IM adapters and send messages to bound IM channels in this workspace. " +
		"Actions: status, mute/unmute, disable/enable, send, send_file. " +
		"Adapters here: ")
	for i, b := range bound {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(b.Adapter)
		if mediaCapable[strings.ToLower(b.Platform)] {
			sb.WriteString(" (" + b.Platform + ", media upload)")
		} else {
			sb.WriteString(" (" + b.Platform + ", text only)")
		}
	}
	sb.WriteString(". mute drops connection but keeps binding for fast restore; disable moves binding to disabled state. " +
		"send_file pushes a local image file as media on media-capable adapters (other files arrive as path text). " +
		"Always allowed in every permission mode.")
	return sb.String()
}

func (t IMTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "mute", "unmute", "disable", "enable", "send", "send_file"],
				"description": "The action to perform."
			},
			"adapter": {
				"type": "string",
				"description": "The adapter name (required for mute/unmute/disable/enable/send). Use 'status' action to find adapter names."
			},
			"message": {
				"type": "string",
				"description": "The text message to send (required for 'send' action)."
			},
			"auto_start": {
				"type": "boolean",
				"description": "(send/send_file) If true, automatically unmute/enable a muted/disabled adapter before sending. Default: false. When true, checks for multi-instance conflicts first.",
				"default": false
			},
			"path": {
				"type": "string",
				"description": "(send_file only) Absolute path to the local file to push (e.g. /tmp/screenshot.png)."
			},
			"caption": {
				"type": "string",
				"description": "(send_file only) Optional one-line caption sent alongside the file."
			}
		},
		"required": ["action"]
	}`)
}

func (t IMTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Manager == nil {
		return Result{IsError: true, Content: "IM is not configured. No IM manager available."}, nil
	}

	var args struct {
		Action    string `json:"action"`
		Adapter   string `json:"adapter"`
		Message   string `json:"message"`
		AutoStart bool   `json:"auto_start"`
		Path      string `json:"path"`
		Caption   string `json:"caption"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := strings.ToLower(strings.TrimSpace(args.Action))
	adapter := strings.TrimSpace(args.Adapter)

	switch action {
	case "status":
		return t.doStatus(), nil
	case "mute":
		return t.doMute(adapter)
	case "unmute":
		return t.doUnmute(adapter)
	case "disable":
		return t.doDisable(adapter)
	case "enable":
		return t.doEnable(adapter)
	case "send":
		return t.doSend(ctx, adapter, args.Message, args.AutoStart)
	case "send_file":
		return t.doSendFile(ctx, adapter, args.Path, args.Caption, args.AutoStart)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q. Valid actions: status, mute, unmute, disable, enable, send, send_file", action)}, nil
	}
}

func (t IMTool) doStatus() Result {
	snap := t.Manager.Snapshot()

	type adapterInfo struct {
		Name      string `json:"name"`
		Platform  string `json:"platform"`
		Healthy   bool   `json:"healthy"`
		Status    string `json:"status"`
		Muted     bool   `json:"muted"`
		Disabled  bool   `json:"disabled"`
		ChannelID string `json:"channel_id,omitempty"`
		LastError string `json:"last_error,omitempty"`
	}

	bindingMap := make(map[string]IMChannelBinding)
	for _, b := range snap.CurrentBindings {
		bindingMap[b.Adapter] = b
	}
	disabledSet := make(map[string]bool)
	for _, b := range snap.DisabledBindings {
		disabledSet[b.Adapter] = true
	}
	adapterStateMap := make(map[string]IMAdapterState)
	for _, a := range snap.Adapters {
		adapterStateMap[a.Name] = a
	}

	allNames := make(map[string]bool)
	for name := range bindingMap {
		allNames[name] = true
	}
	for name := range disabledSet {
		allNames[name] = true
	}
	for name := range adapterStateMap {
		allNames[name] = true
	}

	var adapters []adapterInfo
	for name := range allNames {
		info := adapterInfo{Name: name}
		if state, ok := adapterStateMap[name]; ok {
			info.Platform = state.Platform
			info.Healthy = state.Healthy
			info.Status = state.Status
			info.LastError = state.LastError
		}
		if b, ok := bindingMap[name]; ok {
			info.Platform = firstNonEmptyStr(b.Platform, info.Platform)
			info.Muted = b.Muted
			info.ChannelID = b.ChannelID
		}
		if disabledSet[name] {
			info.Disabled = true
		}
		if info.Status == "" {
			if info.Disabled {
				info.Status = "disabled"
			} else if info.Muted {
				info.Status = "muted"
			} else if info.Healthy {
				info.Status = "connected"
			} else {
				info.Status = "disconnected"
			}
		}
		adapters = append(adapters, info)
	}

	for i := 0; i < len(adapters); i++ {
		for j := i + 1; j < len(adapters); j++ {
			if adapters[i].Name > adapters[j].Name {
				adapters[i], adapters[j] = adapters[j], adapters[i]
			}
		}
	}

	if len(adapters) == 0 {
		return Result{Content: "No IM adapters configured for this workspace."}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("IM Adapters (%d):\n\n", len(adapters)))

	if t.Manager.OtherInstancesHaveActiveChannels() {
		sb.WriteString("  Note: Other instances in this workspace have active IM channels.\n  Starting a competing adapter may cause conflicts (duplicate connections).\n\n")
	}
	for _, a := range adapters {
		stateIcon := "disconnected"
		if a.Disabled {
			stateIcon = "disabled"
		} else if a.Muted {
			stateIcon = "muted"
		} else if a.Healthy {
			stateIcon = "connected"
		}
		sb.WriteString(fmt.Sprintf("  %s [%s] - %s", a.Name, a.Platform, stateIcon))
		if a.ChannelID != "" {
			sb.WriteString(fmt.Sprintf(" (channel: %s)", truncateStr(a.ChannelID, 30)))
		}
		if a.LastError != "" {
			sb.WriteString(fmt.Sprintf(" error: %s", truncateStr(a.LastError, 60)))
		}
		sb.WriteString("\n")
	}
	return Result{Content: strings.TrimSpace(sb.String())}
}

func (t IMTool) doMute(adapter string) (Result, error) {
	if adapter == "" {
		return Result{IsError: true, Content: "adapter name is required for mute action"}, nil
	}
	if err := t.Manager.MuteBinding(adapter); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to mute adapter %q: %v", adapter, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Adapter %q muted. Connection dropped. Use 'unmute' to reconnect.", adapter)}, nil
}

func (t IMTool) doUnmute(adapter string) (Result, error) {
	if adapter == "" {
		return Result{IsError: true, Content: "adapter name is required for unmute action"}, nil
	}
	if err := t.Manager.UnmuteBinding(adapter); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to unmute adapter %q: %v", adapter, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Adapter %q unmuted. Reconnecting...", adapter)}, nil
}

func (t IMTool) doDisable(adapter string) (Result, error) {
	if adapter == "" {
		return Result{IsError: true, Content: "adapter name is required for disable action"}, nil
	}
	if err := t.Manager.DisableBinding(adapter); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to disable adapter %q: %v", adapter, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Adapter %q disabled. Connection dropped. Use 'enable' to reconnect.", adapter)}, nil
}

func (t IMTool) doEnable(adapter string) (Result, error) {
	if adapter == "" {
		return Result{IsError: true, Content: "adapter name is required for enable action"}, nil
	}
	if err := t.Manager.EnableBinding(adapter); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to enable adapter %q: %v", adapter, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Adapter %q enabled. Reconnecting...", adapter)}, nil
}

func findBinding(snap IMSnapshot, adapter string) (binding *IMChannelBinding, isDisabled bool) {
	for i := range snap.CurrentBindings {
		if snap.CurrentBindings[i].Adapter == adapter {
			return &snap.CurrentBindings[i], false
		}
	}
	for i := range snap.DisabledBindings {
		if snap.DisabledBindings[i].Adapter == adapter {
			return &snap.DisabledBindings[i], true
		}
	}
	return nil, false
}

func isAdapterHealthy(snap IMSnapshot, adapter string) bool {
	for _, a := range snap.Adapters {
		if a.Name == adapter {
			return a.Healthy
		}
	}
	return false
}

func (t IMTool) waitForHealthy(ctx context.Context, adapter string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		snap := t.Manager.Snapshot()
		if isAdapterHealthy(snap, adapter) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

func (t IMTool) doSend(ctx context.Context, adapter, message string, autoStart bool) (Result, error) {
	if adapter == "" {
		return Result{IsError: true, Content: "adapter name is required for send action"}, nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return Result{IsError: true, Content: "message text is required for send action"}, nil
	}

	snap := t.Manager.Snapshot()
	binding, isDisabled := findBinding(snap, adapter)
	if binding == nil {
		return Result{IsError: true, Content: fmt.Sprintf("adapter %q has no binding in this workspace. Bind it first via the IM panel.", adapter)}, nil
	}
	if binding.ChannelID == "" {
		return Result{IsError: true, Content: fmt.Sprintf("adapter %q is bound but has no channel ID. Send a message from the IM channel first to complete pairing.", adapter)}, nil
	}

	isMuted := t.Manager.IsBindingMuted(adapter)
	healthy := isAdapterHealthy(snap, adapter)

	if !isMuted && !isDisabled && healthy {
		return t.sendAndReport(ctx, adapter, binding.ChannelID, message)
	}

	if !autoStart {

		stateDesc := "not healthy"
		if isDisabled {
			stateDesc = "disabled"
		} else if isMuted {
			stateDesc = "muted"
		}
		return Result{IsError: true, Content: fmt.Sprintf(
			"adapter %q is %s. Set auto_start=true to automatically activate it before sending.",
			adapter, stateDesc,
		)}, nil
	}

	if t.Manager.OtherInstancesHaveActiveChannels() {

		return Result{IsError: true, Content: fmt.Sprintf(
			"Cannot auto-start adapter %q: another instance in this workspace already has active IM channels. "+
				"Starting a competing connection would cause conflicts (duplicate connections). "+
				"Either mute/disable the adapter on the other instance first, or send the message from that instance.",
			adapter,
		)}, nil
	}

	var activateErr error
	if isDisabled {
		activateErr = t.Manager.EnableBinding(adapter)
	} else if isMuted {
		activateErr = t.Manager.UnmuteBinding(adapter)
	}
	if activateErr != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to activate adapter %q: %v", adapter, activateErr)}, nil
	}

	activated := isDisabled || isMuted
	if !activated {

		if t.waitForHealthy(ctx, adapter, 2*time.Second) {
			return t.sendAndReport(ctx, adapter, binding.ChannelID, message)
		}
		return Result{IsError: true, Content: fmt.Sprintf(
			"adapter %q is unhealthy (not muted/disabled - nothing to auto-activate). Use the IM status tool to inspect it; the message was not sent.",
			adapter,
		)}, nil
	}

	const healthTimeout = 15 * time.Second
	if !t.waitForHealthy(ctx, adapter, healthTimeout) {
		return Result{IsError: true, Content: fmt.Sprintf(
			"adapter %q was activated but did not become healthy within %s. The message was not sent. "+
				"Check adapter status for errors.",
			adapter, healthTimeout,
		)}, nil
	}

	return t.sendAndReport(ctx, adapter, binding.ChannelID, message)
}

func (t IMTool) sendAndReport(ctx context.Context, adapter, channelID, message string) (Result, error) {
	err := t.Manager.SendDirect(ctx, adapter, IMOutboundEvent{Kind: "text", Text: message})
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to send message via %q: %v", adapter, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Message sent via %s (channel: %s).", adapter, truncateStr(channelID, 30))}, nil
}

const sendFileMaxBytes = 20 * 1024 * 1024

func sendFileMIME(ext string, data []byte) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".log", ".md":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".zip":
		return "application/zip"
	case ".gz":
		return "application/gzip"
	case ".tar":
		return "application/x-tar"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	}
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	if m := http.DetectContentType(sniff); m != "" && m != "application/octet-stream" {
		return m
	}
	return "application/octet-stream"
}

var sendFileImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
}

var sendFileExamplePath = func() string {
	if runtime.GOOS == "windows" {
		return `C:\Users\me\Pictures\screenshot.png`
	}
	return "/tmp/screenshot.png"
}()

func (t IMTool) doSendFile(ctx context.Context, adapter, path, caption string, autoStart bool) (Result, error) {
	path = strings.TrimSpace(path)
	if adapter == "" {
		return Result{IsError: true, Content: "adapter name is required for send_file action"}, nil
	}
	if path == "" {
		return Result{IsError: true, Content: "file path is required for send_file action"}, nil
	}

	if !filepath.IsAbs(path) && !strings.HasPrefix(path, "/") {
		return Result{IsError: true, Content: fmt.Sprintf("path %q must be absolute (e.g. %s)", path, sendFileExamplePath)}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("file not accessible: %v", err)}, nil
	}
	if info.IsDir() {
		return Result{IsError: true, Content: fmt.Sprintf("%q is a directory; send_file pushes a single file", path)}, nil
	}
	if info.Size() > sendFileMaxBytes {
		return Result{IsError: true, Content: fmt.Sprintf("file is %d bytes; send_file caps at %d bytes", info.Size(), sendFileMaxBytes)}, nil
	}

	ext := strings.ToLower(filepath.Ext(path))
	delivery := "file path (this adapter has no media upload)"
	if sendFileImageExts[ext] {
		delivery = "image media upload"
	}

	isMuted := t.Manager.IsBindingMuted(adapter)
	isDisabled := t.Manager.IsBindingDisabled(adapter)
	snap := t.Manager.Snapshot()
	if !isMuted && !isDisabled && isAdapterHealthy(snap, adapter) && info.Size() <= sendFileMaxBytes {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("reading file: %v", readErr)}, nil
		}
		mimeType := sendFileMIME(ext, data)
		file := IMOutboundFile{
			Path:     path,
			Filename: filepath.Base(path),
			MIME:     mimeType,
			Data:     data,
		}
		supported, sendErr := t.Manager.SendFileDirect(ctx, adapter, file, strings.TrimSpace(caption))
		if sendErr != nil {
			return Result{IsError: true, Content: fmt.Sprintf("failed to upload file via %q: %v", adapter, sendErr)}, nil
		}
		if supported {
			kind := "file"
			if strings.HasPrefix(mimeType, "image/") {
				kind = "image"
			}
			return Result{Content: fmt.Sprintf("File %s (%s, %d bytes) uploaded via %s as %s.", filepath.Base(path), mimeType, info.Size(), adapter, kind)}, nil
		}

	}

	var sb strings.Builder
	if c := strings.TrimSpace(caption); c != "" {
		sb.WriteString(c + "\n")
	}
	sb.WriteString(path)
	note := ""
	if !sendFileImageExts[ext] {
		note = fmt.Sprintf(" Note: %s files are delivered as the file path text; only images (png/jpg/jpeg/gif/webp) are uploaded as media.", strings.TrimPrefix(ext, "."))
	}
	res, err := t.doSend(ctx, adapter, sb.String(), autoStart)
	if err != nil || res.IsError {
		return res, err
	}
	if res.Content != "" {
		res.Content = fmt.Sprintf("File sent via %s (%s).%s", adapter, delivery, note)
	}
	return res, nil
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncateStr(s string, max int) string {
	return util.Truncate(s, max)
}
