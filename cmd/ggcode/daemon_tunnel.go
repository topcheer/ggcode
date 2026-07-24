package main

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func daemonToolDisplayName(toolName, rawArgs string) string {
	if toolName == "swarm_task_create" {
		if subject := tool.SwarmTaskCreateSubject(rawArgs); strings.TrimSpace(subject) != "" {
			return strings.TrimSpace(subject)
		}
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawArgs), &args); err == nil {
		if v, ok := args["description"]; ok {
			var desc string
			if json.Unmarshal(v, &desc) == nil && strings.TrimSpace(desc) != "" {
				return strings.TrimSpace(desc)
			}
		}
	}
	toolName = strings.ReplaceAll(toolName, "-", " ")
	toolName = strings.ReplaceAll(toolName, "_", " ")
	parts := strings.Fields(toolName)
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(string([]rune(part)[:1])) + string([]rune(part)[1:])
	}
	return strings.Join(parts, " ")
}

type daemonTunnelCommandTarget interface {
	SendUserMessage(content []provider.ContentBlock)
	InterruptActiveRun() bool
}

type daemonTunnelBroker interface {
	NextMessageID() string
	PushText(msgID, text string)
	PushTextDone(msgID string)
	PushReasoning(msgID, text string)
	PushReasoningDone(msgID string)
	PushToolCall(toolID, toolName, displayName, rawArgs, detail string)
	PushToolResult(toolID, toolName, result string, isError bool)
	PushStatus(status, message string)
	PushError(message string)
	PushUserMessageData(data tunnel.MessageData)
	PushServerAck(messageID string)
}

type daemonTunnelShareController struct {
	broker      daemonTunnelBroker
	bridge      *im.DaemonBridge
	sessionInfo tunnel.SessionInfoData
	tunnelHost  *agentruntime.TunnelHost

	mu            sync.Mutex
	currentMsgID  string
	needsFinalize bool
	reasoningTail string
	textTail      string
	status        tunnel.StatusData
	userOverrides []tunnel.MessageData // queue, not single override — prevents message loss
}

func newDaemonTunnelShareController(broker daemonTunnelBroker, bridge *im.DaemonBridge, sessionInfo tunnel.SessionInfoData, tunnelHost *agentruntime.TunnelHost) *daemonTunnelShareController {
	status := tunnel.StatusData{Status: tunnel.StatusIdle}
	if bridge != nil && bridge.HasActiveRun() {
		status.Status = tunnel.StatusBusy
	}
	return &daemonTunnelShareController{
		broker:      broker,
		bridge:      bridge,
		sessionInfo: sessionInfo,
		status:      status,
		tunnelHost:  tunnelHost,
	}
}

func (c *daemonTunnelShareController) PrepareBroker(broker *tunnel.Broker, target daemonTunnelCommandTarget, ses *session.Session) {
	if c == nil || broker == nil || target == nil {
		return
	}

	// Attach online broker to unified TunnelHost so PushStreamEvent forwards events
	if c.tunnelHost != nil {
		c.tunnelHost.AttachOnlineBroker(broker)
	}

	broker.OnCommand(func(cmd tunnel.GatewayMessage) {
		c.HandleCommand(target, cmd)
	})
	broker.SetSnapshotProvider(func() tunnel.BrokerSnapshot {
		return c.Snapshot()
	})

	sessionID := ""
	var replay []tunnel.GatewayMessage
	if ses != nil {
		sessionID = ses.ID
	}
	if sessionID != "" {
		// Use TunnelHost's projection store for replay if available
		if c.tunnelHost != nil {
			if events := c.tunnelHost.TunnelEvents(); events != nil {
				replay = events
				broker.SetAuthorityEpoch(c.tunnelHost.AuthorityEpoch())
			}
			// Recording is handled by TunnelHost's BindSession event recorder,
			// forwarded to online broker via AttachOnlineBroker above.
			// Cache session info and run canonical share bootstrap.
			c.tunnelHost.SetSessionInfo(c.sessionInfo)
			c.tunnelHost.PrepareOnlineShare(broker)
		} else {
			// Legacy fallback: create local projection store
			if store, err := tunnel.NewDefaultProjectionStore(); err == nil {
				broker.SetReplayProvider(func() []tunnel.GatewayMessage {
					events, err := agentruntime.ProjectionReplay(store, sessionID)
					if err != nil {
						return nil
					}
					return events
				})
				broker.SetEventRecorder(func(ev tunnel.GatewayMessage) {
					_ = agentruntime.AppendProjectionEvent(store, ev)
				})
				if epoch, events, err := agentruntime.PrepareProjectionReplay(store, ses); err == nil {
					broker.SetAuthorityEpoch(epoch)
					replay = events
				}
			}
		}
	} else {
		// Legacy path (no TunnelHost): use PublishShareState for broker setup.
		agentruntime.PublishShareState(broker, sessionID, c.Snapshot(), replay, true)
	}
}

func (c *daemonTunnelShareController) Snapshot() tunnel.BrokerSnapshot {
	snapshot := tunnel.BrokerSnapshot{
		SessionInfo: c.sessionInfo,
		Status:      c.currentStatus(),
	}
	if c.bridge != nil {
		history := daemonTunnelMessagesToHistory(c.bridge.Messages())
		if tail := c.currentIncompleteHistoryTail(); len(tail) > 0 {
			history = append(history, tail...)
		}
		if len(history) > 0 {
			snapshot.History = history
		}
	}
	return snapshot
}

// daemonSnapshot builds a BrokerSnapshot from daemon bridge state for StartShare.
func daemonSnapshot(bridge *im.DaemonBridge, workspace string, resolved *config.ResolvedEndpoint, mode string) tunnel.BrokerSnapshot {
	snapshot := tunnel.BrokerSnapshot{
		SessionInfo: tunnel.SessionInfoData{
			Workspace: workspace,
			Mode:      mode,
		},
	}
	model := ""
	vendorName := ""
	if resolved != nil {
		model = resolved.Model
		vendorName = resolved.VendorName
	}
	snapshot.SessionInfo.Model = model
	snapshot.SessionInfo.Provider = vendorName

	if bridge != nil {
		status := tunnel.StatusIdle
		if bridge.HasActiveRun() {
			status = tunnel.StatusBusy
		}
		snapshot.Status = tunnel.StatusData{Status: status}
		history := daemonTunnelMessagesToHistory(bridge.Messages())
		if len(history) > 0 {
			snapshot.History = history
		}
	}
	return snapshot
}

func (c *daemonTunnelShareController) HandleRunState(busy bool) {
	status := tunnel.StatusIdle
	if busy {
		status = tunnel.StatusBusy
	}
	c.setStatus(status, "")
}

func (c *daemonTunnelShareController) HandleUserMessage(content []provider.ContentBlock) {
	if c == nil || c.broker == nil {
		return
	}
	data := c.consumeUserMessageOverride()
	if data.Text == "" {
		data = daemonTunnelMessageDataFromContent(content)
	}
	if strings.TrimSpace(data.Text) == "" {
		return
	}
	c.broker.PushUserMessageData(data)
}

func (c *daemonTunnelShareController) SetNextUserMessageOverride(data tunnel.MessageData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data.MessageID = tunnel.NormalizeClientMessageID(data.MessageID)
	c.userOverrides = append(c.userOverrides, data)
}

func (c *daemonTunnelShareController) HandleCommand(target daemonTunnelCommandTarget, cmd tunnel.GatewayMessage) {
	if c == nil || c.broker == nil || target == nil {
		return
	}
	agentruntime.RouteTunnelCommand(cmd, agentruntime.TunnelCommandHooks{
		OnUserMessage: func(data tunnel.MessageData) {
			c.SetNextUserMessageOverride(data)
			target.SendUserMessage([]provider.ContentBlock{{Type: "text", Text: data.Text}})
		},
		OnInterrupt: func() {
			if !target.InterruptActiveRun() {
				return
			}
			c.cancelCurrentRun()
		},
		OnServerAck: func(messageID string) {
			c.broker.PushServerAck(messageID)
		},
	})
}

func (c *daemonTunnelShareController) HandleStreamEvent(ev provider.StreamEvent) {
	if c == nil {
		return
	}

	// Daemon-specific: update run state for status push
	switch ev.Type {
	case provider.StreamEventText, provider.StreamEventReasoning,
		provider.StreamEventToolCallDone, provider.StreamEventToolResult,
		provider.StreamEventSystem:
		c.HandleRunState(true)
	case provider.StreamEventDone, provider.StreamEventError:
		c.HandleRunState(false)
	}

	// Delegate stream push to unified TunnelHost
	if c.tunnelHost != nil {
		c.tunnelHost.PushStreamEvent(ev)
	}
}

func (c *daemonTunnelShareController) currentStatus() tunnel.StatusData {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *daemonTunnelShareController) consumeUserMessageOverride() tunnel.MessageData {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.userOverrides) == 0 {
		return tunnel.MessageData{}
	}
	data := c.userOverrides[0]
	c.userOverrides = c.userOverrides[1:]
	return data
}

func (c *daemonTunnelShareController) currentIncompleteHistoryTail() []tunnel.HistoryEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return daemonTunnelHistoryTail(c.reasoningTail, c.textTail)
}

func (c *daemonTunnelShareController) setStatus(status, message string) {
	if c == nil || c.broker == nil {
		return
	}
	c.mu.Lock()
	if c.status.Status == status && c.status.Message == message {
		c.mu.Unlock()
		return
	}
	c.status = tunnel.StatusData{Status: status, Message: message}
	c.mu.Unlock()
	c.broker.PushStatus(status, message)
}

func (c *daemonTunnelShareController) cancelCurrentRun() {
	c.rolloverMainStream(true)
	c.setStatus(tunnel.StatusIdle, "cancelled")
}

func (c *daemonTunnelShareController) rolloverMainStream(force bool) {
	if c == nil || c.broker == nil {
		return
	}
	c.mu.Lock()
	msgID := strings.TrimSpace(c.currentMsgID)
	needsFinalize := c.needsFinalize
	c.currentMsgID = ""
	c.needsFinalize = false
	c.reasoningTail = ""
	c.textTail = ""
	c.mu.Unlock()
	if msgID == "" {
		return
	}
	c.broker.PushReasoningDone(agentruntime.TunnelReasoningMsgID(msgID))
	if !force && !needsFinalize {
		return
	}
	c.broker.PushTextDone(msgID)
}

func daemonTunnelHistoryTail(reasoning, text string) []tunnel.HistoryEntry {
	var history []tunnel.HistoryEntry
	if reasoning = strings.TrimSpace(reasoning); reasoning != "" {
		history = append(history, tunnel.HistoryEntry{Role: "reasoning", Content: reasoning})
	}
	if text = strings.TrimSpace(text); text != "" {
		history = append(history, tunnel.HistoryEntry{Role: "assistant", Content: text})
	}
	return history
}

func daemonTunnelMessageDataFromContent(content []provider.ContentBlock) tunnel.MessageData {
	var textParts []string
	for _, block := range content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			textParts = append(textParts, strings.TrimSpace(block.Text))
		}
	}
	if len(textParts) == 0 {
		return tunnel.MessageData{}
	}
	return tunnel.MessageData{Text: strings.Join(textParts, "\n")}
}

func daemonTunnelMessagesToHistory(msgs []provider.Message) []tunnel.HistoryEntry {
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
					history = append(history, tunnel.HistoryEntry{
						Role:     "tool_result",
						ToolID:   block.ToolID,
						ToolName: block.ToolName,
						Result:   daemonTruncateRunes(block.Output, 500, "..."),
						IsError:  block.IsError,
					})
				}
			}
			if len(textParts) > 0 {
				history = append(history, tunnel.HistoryEntry{Role: "user", Content: strings.Join(textParts, "\n")})
			}
		case "assistant":
			for _, block := range msg.Content {
				if reasoning := daemonContentBlockReasoningText(block); reasoning != "" {
					history = append(history, tunnel.HistoryEntry{Role: "reasoning", Content: reasoning})
				}
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						history = append(history, tunnel.HistoryEntry{Role: "assistant", Content: strings.TrimSpace(block.Text)})
					}
				case "tool_use":
					present := tool.DescribeTool(block.ToolName, string(block.Input))
					history = append(history, tunnel.HistoryEntry{
						Role:            "tool_call",
						ToolID:          block.ToolID,
						ToolName:        block.ToolName,
						ToolDisplayName: daemonToolDisplayName(block.ToolName, string(block.Input)),
						ToolArgs:        daemonTruncateRunes(string(block.Input), 200, "..."),
						ToolDetail:      present.Detail,
					})
				}
			}
		case "tool":
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					history = append(history, tunnel.HistoryEntry{
						Role:     "tool_result",
						ToolID:   block.ToolID,
						ToolName: block.ToolName,
						Result:   daemonTruncateRunes(block.Output, 500, "..."),
						IsError:  block.IsError,
					})
				}
			}
		}
	}
	return history
}

func daemonContentBlockReasoningText(block provider.ContentBlock) string {
	if text := tunnel.NormalizeReasoningChunk(block.ReasoningContent); text != "" {
		return text
	}
	if strings.TrimSpace(block.ThinkingData) != "" {
		return tunnel.RedactedReasoningPlaceholder
	}
	return ""
}

func daemonTruncateRunes(s string, maxRunes int, suffix string) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	suffixRunes := []rune(suffix)
	if len(suffixRunes) >= maxRunes {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-len(suffixRunes)]) + suffix
}
