package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
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
	return "Manage IM (Instant Messaging) adapters and send messages to bound channels. " +
		"Supports Telegram, Discord, Slack, DingTalk, Feishu, WeChat, IRC, Matrix, QQ, Nostr, Signal, WhatsApp, Mattermost, and Twitch.\n\n" +
		"Actions:\n" +
		"- 'status': List all IM adapters with health, platform, muted/disabled state, and channel binding info for the current workspace.\n" +
		"- 'mute': Mute a specific adapter. Drops the connection (inbound and outbound messages stop). The binding stays so the adapter can be quickly resumed.\n" +
		"- 'unmute': Unmute a previously muted adapter. Reconnects and resumes message flow.\n" +
		"- 'disable': Disable a specific adapter. Moves the binding to disabled state and drops the connection. More aggressive than mute.\n" +
		"- 'enable': Re-enable a previously disabled adapter. Reconnects.\n" +
		"- 'send': Send a text message to a specific adapter's bound channel.\n" +
		"  - By default the adapter must be active (unmuted + enabled + healthy).\n" +
		"  - Set auto_start=true to automatically unmute/enable a muted/disabled adapter before sending.\n" +
		"  - When auto_start=true, checks if other instances in the same workspace already have this adapter active. If so, does NOT start a competing connection (would cause conflicts) — instead reports the situation.\n\n" +
		"Guidelines:\n" +
		"- Check 'status' first to see which adapters are configured and their current state.\n" +
		"- 'mute' drops the connection just like 'disable', but keeps the binding in the active list for faster restoration.\n" +
		"- 'send' only delivers to the adapter's bound channel — you cannot specify an arbitrary recipient.\n" +
		"- This tool is always allowed in every permission mode."
}

func (t IMTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["status", "mute", "unmute", "disable", "enable", "send"],
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
				"description": "(send only) If true, automatically unmute/enable a muted/disabled adapter before sending. Default: false. When true, checks for multi-instance conflicts first.",
				"default": false
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
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q. Valid actions: status, mute, unmute, disable, enable, send", action)}, nil
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
		stateDesc := "muted"
		if isDisabled {
			stateDesc = "disabled"
		} else if !healthy {
			stateDesc = "not healthy"
		}
		return Result{IsError: true, Content: fmt.Sprintf(
			"adapter %q is %s. Set auto_start=true to automatically activate it before sending.",
			adapter, stateDesc,
		)}, nil
	}

	// Case 3: auto_start=true — check multi-instance conflict first
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

	// Case 4: auto_start=true, no conflict — activate the adapter
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

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
