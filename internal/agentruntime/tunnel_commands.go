package agentruntime

import (
	"encoding/json"
	"strings"

	"github.com/topcheer/ggcode/internal/tunnel"
)

type TunnelCommandHooks struct {
	OnUserMessage      func(data tunnel.MessageData)
	OnInterrupt        func()
	OnModeChange       func(data tunnel.ModeChangeData)
	OnApprovalResponse func(data tunnel.ApprovalResponseData)
	OnAskUserResponse  func(data tunnel.AskUserResponseData)
	OnLanguageChange   func(data tunnel.LanguageChangeData)
	OnThemeChange      func(data tunnel.ThemeChangeData)
	OnServerAck        func(messageID string)
}

func RouteTunnelCommand(cmd tunnel.GatewayMessage, hooks TunnelCommandHooks) {
	switch cmd.Type {
	case tunnel.CmdMessage, "user_text":
		var data tunnel.MessageData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if strings.TrimSpace(data.Text) == "" {
			return
		}
		data.MessageID = tunnel.NormalizeClientMessageID(data.MessageID)
		if hooks.OnUserMessage != nil {
			hooks.OnUserMessage(data)
		}
		if hooks.OnServerAck != nil {
			hooks.OnServerAck(data.MessageID)
		}
	case tunnel.CmdInterrupt:
		if hooks.OnInterrupt != nil {
			hooks.OnInterrupt()
		}
	case tunnel.CmdModeChange:
		if hooks.OnModeChange == nil {
			return
		}
		var data tunnel.ModeChangeData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		hooks.OnModeChange(data)
	case tunnel.CmdApprovalResponse:
		if hooks.OnApprovalResponse == nil {
			return
		}
		var data tunnel.ApprovalResponseData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		hooks.OnApprovalResponse(data)
	case tunnel.CmdAskUserResponse:
		if hooks.OnAskUserResponse == nil {
			return
		}
		var data tunnel.AskUserResponseData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		hooks.OnAskUserResponse(data)
	case tunnel.CmdLanguageChange:
		if hooks.OnLanguageChange == nil {
			return
		}
		var data tunnel.LanguageChangeData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if data.Language != "" {
			hooks.OnLanguageChange(data)
		}
	case tunnel.CmdThemeChange:
		if hooks.OnThemeChange == nil {
			return
		}
		var data tunnel.ThemeChangeData
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return
		}
		if data.Theme != "" {
			hooks.OnThemeChange(data)
		}
	}
}
