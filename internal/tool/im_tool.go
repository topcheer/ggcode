package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// IMManager is the subset of im.Manager methods needed by the IM tool.
// Defined here to avoid importing internal/im (which would create a cycle
// via internal/config).
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
	OtherInstancesHaveActiveChannels() bool
}

// IMSnapshot is a subset of im.StatusSnapshot.
type IMSnapshot struct {
	CurrentBindings  []IMChannelBinding
	DisabledBindings []IMChannelBinding
	Adapters         []IMAdapterState
}

// IMChannelBinding is a subset of im.ChannelBinding.
type IMChannelBinding struct {
	Adapter   string
	Platform  string
	ChannelID string
	Muted     bool
}

// IMAdapterState is a subset of im.AdapterState.
type IMAdapterState struct {
	Name      string
	Platform  string
	Healthy   bool
	Status    string
	LastError string
}

// IMOutboundEvent is a subset of im.OutboundEvent for sending text.
type IMOutboundEvent struct {
	Kind string
	Text string
}

// IMTool lets the LLM manage IM adapters and send messages.
// The manager is injected post-registration via SetManager().
type IMTool struct {
	Manager IMManager
}

func (t IMTool) Name() string { return "im" }

func (t IMTool) Description() string {
	// Registered unconditionally in builtin.go, but many workspaces have no
	// IM configured: shrink the description instead of advertising actions
	// and platforms that cannot work (generic descriptions waste context and
	// invite calls against nonexistent adapters).
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

	// List only the adapters that actually exist here, with per-adapter
	// media capability, so the LLM knows which target names are real and
	// whether send_file uploads media or degrades to path text.
	mediaCapable := map[string]bool{
		"qq": true, "telegram": true, "discord": true, "feishu": true,
		"matrix": true, "whatsapp": true, "slack": true, "mattermost": true,
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

	// Build a map of adapter -> binding for cross-referencing
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

	// Collect all adapter names from both bindings and adapter states
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

	// Sort by name
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
	// Show multi-instance info
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

// findBinding searches both current and disabled bindings for the named adapter.
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

// isAdapterHealthy checks if the adapter is in a healthy/connected state.
func isAdapterHealthy(snap IMSnapshot, adapter string) bool {
	for _, a := range snap.Adapters {
		if a.Name == adapter {
			return a.Healthy
		}
	}
	return false
}

// waitForHealthy polls Snapshot until the adapter becomes healthy or timeout.
func (t IMTool) waitForHealthy(adapter string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
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

	// Case 1: adapter is active and healthy -> send directly
	if !isMuted && !isDisabled && healthy {
		return t.sendAndReport(ctx, adapter, binding.ChannelID, message)
	}

	// Case 2: adapter is muted or disabled, and auto_start is false
	if !autoStart {
		// #848: muted adapters are almost always 'unhealthy' too - checking
		// health first pointed operators at connection debugging instead of
		// unmute/auto_start. Mute is the actionable state; check it first.
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

	// Case 3: auto_start=true - check multi-instance conflict first
	if t.Manager.OtherInstancesHaveActiveChannels() {
		// Another instance in the same workspace has active channels.
		// Starting a competing adapter connection would cause conflicts
		// (e.g. Telegram bot duplicate polling, Discord gateway clash).
		return Result{IsError: true, Content: fmt.Sprintf(
			"Cannot auto-start adapter %q: another instance in this workspace already has active IM channels. "+
				"Starting a competing connection would cause conflicts (duplicate connections). "+
				"Either mute/disable the adapter on the other instance first, or send the message from that instance.",
			adapter,
		)}, nil
	}

	// Case 4: auto_start=true, no conflict - activate the adapter
	var activateErr error
	if isDisabled {
		activateErr = t.Manager.EnableBinding(adapter)
	} else if isMuted {
		activateErr = t.Manager.UnmuteBinding(adapter)
	}
	if activateErr != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to activate adapter %q: %v", adapter, activateErr)}, nil
	}

	// Wait for adapter to become healthy (max 15 seconds)
	const healthTimeout = 15 * time.Second
	if !t.waitForHealthy(adapter, healthTimeout) {
		return Result{IsError: true, Content: fmt.Sprintf(
			"adapter %q was activated but did not become healthy within %s. The message was not sent. "+
				"Check adapter status for errors.",
			adapter, healthTimeout,
		)}, nil
	}

	return t.sendAndReport(ctx, adapter, binding.ChannelID, message)
}

// sendAndReport sends the message via SendDirect and returns the result.
func (t IMTool) sendAndReport(ctx context.Context, adapter, channelID, message string) (Result, error) {
	err := t.Manager.SendDirect(ctx, adapter, IMOutboundEvent{Kind: "text", Text: message})
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to send message via %q: %v", adapter, err)}, nil
	}
	return Result{Content: fmt.Sprintf("Message sent via %s (channel: %s).", adapter, truncateStr(channelID, 30))}, nil
}

// sendFileMaxBytes caps the file size accepted by send_file. Image uploads
// are already capped at image.MaxSize (20MB) inside the adapters; non-image
// files travel as path text and need no cap, but validating early gives the
// LLM an immediate, actionable error instead of an adapter-side failure.
const sendFileMaxBytes = 20 * 1024 * 1024

// sendFileImageExts lists extensions that every media-capable adapter
// (qq/telegram/discord/feishu/matrix/whatsapp/slack/mattermost) can upload
// as rich media today. Other extensions are delivered as the file path text.
var sendFileImageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
}

// doSendFile pushes a local file to a bound IM channel. The file path is
// sent as the message text: media-capable adapters extract image paths via
// ExtractImagesFromText and upload them as rich media (the extraction layer
// added local-path support for exactly this flow); non-image files arrive
// as a clickable path line, which is the honest cross-platform baseline.
// Reuses doSend's full activation/health/multi-instance-conflict pipeline.
func (t IMTool) doSendFile(ctx context.Context, adapter, path, caption string, autoStart bool) (Result, error) {
	path = strings.TrimSpace(path)
	if adapter == "" {
		return Result{IsError: true, Content: "adapter name is required for send_file action"}, nil
	}
	if path == "" {
		return Result{IsError: true, Content: "file path is required for send_file action"}, nil
	}
	if !filepath.IsAbs(path) {
		return Result{IsError: true, Content: fmt.Sprintf("path %q must be absolute (e.g. /tmp/screenshot.png)", path)}, nil
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

	// Caption first, path on its own line: the extractor matches paths that
	// follow whitespace/newlines, so the path must not be glued to CJK text.
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
