package tui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/safego"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/webui"
)

// #5865: browser tabs (webui) answer ask-type gates through the same
// request/response shape the mobile tunnel uses. The Model mints the request
// id, publishes the request on the webui bridge, and resolves it with the
// decision - whichever surface (TUI key, IM reply, tunnel, webui) won.
// Browser responses arrive as webUIApprovalResponseMsg /
// webUIAskUserResponseMsg via repl.ResolveWebUIInteraction.

type webUIApprovalResponseMsg struct {
	id       string
	decision string
}

type webUIAskUserResponseMsg struct {
	id      string
	status  string
	answers []tunnel.AskUserAnswer
}

func (m *Model) nextWebUIRequestID() string {
	m.webUIReqSeq++
	return "webui-req-" + strconv.Itoa(m.webUIReqSeq)
}

// pushWebUIApprovalRequest mirrors the tunnel push in handleApprovalMsg.
func (m *Model) pushWebUIApprovalRequest(toolName, input string) string {
	ib, ok := m.webuiBridge.(WebUIInteractionBridge)
	if !ok || ib == nil {
		return ""
	}
	id := m.nextWebUIRequestID()
	ib.PublishInteraction(webui.InteractionEvent{
		Kind:     webui.InteractionKindApproval,
		ID:       id,
		ToolName: toolName,
		Input:    input,
	})
	return id
}

func (m *Model) pushWebUIAskUserRequest(req toolpkg.AskUserRequest) string {
	ib, ok := m.webuiBridge.(WebUIInteractionBridge)
	if !ok || ib == nil {
		return ""
	}
	id := m.nextWebUIRequestID()
	ib.PublishInteraction(webui.InteractionEvent{
		Kind:    webui.InteractionKindAskUser,
		ID:      id,
		Request: &req,
	})
	return id
}

// resolveWebUIApproval broadcasts the outcome to tabs (first answer wins;
// late responses find the id gone and get an error ack).
func (m *Model) resolveWebUIApproval(decision string) {
	id := m.webUIPendingApprovalID
	m.webUIPendingApprovalID = ""
	if id == "" {
		return
	}
	if ib, ok := m.webuiBridge.(WebUIInteractionBridge); ok && ib != nil {
		ib.ResolveInteraction(webui.InteractionKindApproval, id, decision)
	}
}

func (m *Model) resolveWebUIAskUser(status string) {
	id := m.webUIPendingAskUserID
	m.webUIPendingAskUserID = ""
	if id == "" {
		return
	}
	if ib, ok := m.webuiBridge.(WebUIInteractionBridge); ok && ib != nil {
		ib.ResolveInteraction(webui.InteractionKindAskUser, id, status)
	}
}

func (m *Model) handleWebUIApprovalResponse(msg webUIApprovalResponseMsg) (tea.Model, tea.Cmd) {
	if m.pendingApproval == nil {
		return m, nil
	}
	if msg.id == "" || (m.webUIPendingApprovalID != "" && msg.id != m.webUIPendingApprovalID) {
		debug.Log("tui", "webui approval response id=%q dropped (current=%q)", msg.id, m.webUIPendingApprovalID)
		return m, nil
	}
	switch msg.decision {
	case webui.InteractionDecisionAllow:
		return m, m.handleApproval(permission.Allow)
	case webui.InteractionDecisionAlwaysAllow, "always":
		return m, m.handleApprovalAllowAlways()
	case webui.InteractionDecisionDeny:
		return m, m.handleApproval(permission.Deny)
	default:
		debug.Log("tui", "webui approval response with unknown decision %q dropped", msg.decision)
		return m, nil
	}
}

func (m *Model) handleWebUIAskUserResponse(msg webUIAskUserResponseMsg) (tea.Model, tea.Cmd) {
	qs := m.pendingQuestionnaire
	if qs == nil {
		return m, nil
	}
	if msg.id == "" || (m.webUIPendingAskUserID != "" && msg.id != m.webUIPendingAskUserID) {
		debug.Log("tui", "webui ask_user response id=%q dropped (current=%q)", msg.id, m.webUIPendingAskUserID)
		return m, nil
	}
	status := msg.status
	if status == "" {
		status = toolpkg.AskUserStatusSubmitted
	}
	resp := agentruntime.BuildAskUserResponseFromTunnel(qs.request, status, msg.answers)
	m.pendingQuestionnaire = nil
	m.resolveWebUIAskUser(status)
	m.pushTunnelAskUserResponse(m.tunnelPendingAskUserID, resp)
	m.tunnelPendingAskUserID = ""
	m.oscWorking("")
	if qs.response != nil {
		ch := qs.response
		safego.Go("tui.askUser.webuiRespond", func() {
			select {
			case ch <- resp:
			default:
			}
		})
	}
	return m, nil
}
