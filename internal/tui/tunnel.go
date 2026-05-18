package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/tunnel"
)

// tunnelStartMsg is sent when the tunnel is ready.
type tunnelStartMsg struct {
	info    *tunnel.SessionInfo
	session *tunnel.Session
	broker  *tunnel.Broker
	err     error
}

// tunnelStopMsg is sent when the tunnel has stopped.
type tunnelStopMsg struct{}

func (m *Model) handleTunnelCommand(text string) tea.Cmd {
	args := strings.TrimSpace(strings.TrimPrefix(text, "/tunnel"))

	switch args {
	case "stop", "close", "off":
		if m.tunnelSession != nil {
			m.tunnelSession.Stop()
			m.tunnelSession = nil
			m.tunnelBroker = nil
			m.chatWriteSystem(nextSystemID(), "Tunnel closed.")
		} else {
			m.chatWriteSystem(nextSystemID(), "No active tunnel.")
		}
		return nil

	case "status":
		if m.tunnelSession == nil {
			m.chatWriteSystem(nextSystemID(), "No active tunnel. Use /tunnel to start one.")
		} else {
			info := m.tunnelSession.Info()
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Relay active:\n  Connect: %s", info.ConnectURL))
		}
		return nil

	case "", "start", "on":
		if m.tunnelSession != nil {
			info := m.tunnelSession.Info()
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Tunnel already active:\n  URL: %s", info.ConnectURL))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), "Starting tunnel...")
		return m.startTunnel()

	default:
		m.chatWriteSystem(nextSystemID(), "Usage: /tunnel [start|stop|status]")
		return nil
	}
}

func (m *Model) startTunnel() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		sess := tunnel.NewSession(tunnel.DefaultRelayURL)
		info, err := sess.Start(ctx)
		if err != nil {
			return tunnelStartMsg{err: err}
		}

		broker := tunnel.NewBroker(sess)
		broker.OnCommand(func(msg tunnel.GatewayMessage) {
			m.handleTunnelClientMessage(msg)
		})

		return tunnelStartMsg{info: info, session: sess, broker: broker}
	}
}

func (m *Model) handleTunnelClientMessage(msg tunnel.GatewayMessage) {
	switch msg.Type {
	case tunnel.CmdMessage:
		var data tunnel.MessageData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		// TODO: route to agent as user input
	case tunnel.CmdInterrupt:
		if m.pending != nil {
			m.pending.enqueue("/interrupt")
		}
	case tunnel.CmdApprovalResponse:
		var data tunnel.ApprovalResponseData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		_ = data // TODO: route to approval handler
	case tunnel.CmdModeChange:
		var data tunnel.ModeChangeData
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			return
		}
		_ = data // TODO: route to mode handler
	}
}
