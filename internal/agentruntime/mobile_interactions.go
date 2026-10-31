package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

type TunnelStateUpdate struct {
	HasStatus   bool
	Status      string
	StatusMsg   string
	HasActivity bool
	Activity    string
}

func ApplyTunnelStateUpdate(broker *tunnel.Broker, update TunnelStateUpdate) {
	if broker == nil {
		return
	}
	if update.HasStatus {
		broker.PushStatus(update.Status, update.StatusMsg)
	}
	if update.HasActivity {
		broker.PushActivity(update.Activity)
	}
}

func PushTunnelApprovalRequest(broker *tunnel.Broker, requestID, toolName, input string, update TunnelStateUpdate) {
	if broker == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	broker.PushApprovalRequest(requestID, toolName, input)
	ApplyTunnelStateUpdate(broker, update)
}

func PushTunnelAskUserRequest(broker *tunnel.Broker, requestID string, req tool.AskUserRequest, update TunnelStateUpdate) {
	if broker == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	broker.PushAskUserRequest(requestID, req.Title, BuildTunnelAskUserQuestions(req))
	ApplyTunnelStateUpdate(broker, update)
}

func PushTunnelApprovalResult(broker *tunnel.Broker, requestID, decision string, update TunnelStateUpdate) {
	if broker == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	broker.PushApprovalResult(requestID, decision)
	ApplyTunnelStateUpdate(broker, update)
}

func PushTunnelAskUserResponse(broker *tunnel.Broker, requestID string, response tool.AskUserResponse, update TunnelStateUpdate) {
	if broker == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	broker.PushAskUserResponse(requestID, response.Status, BuildTunnelAskUserAnswers(response))
	ApplyTunnelStateUpdate(broker, update)
}

// PushTunnelAskUserResult closes a pending mobile ask-user card after the
// request is gone on the agent side (#4493, the #4250 closure pattern):
// the 15-minute timeout (or a stale late answer) left the card showing
// "waiting" forever. The status stays in the established vocabulary
// ("cancelled") so mobile clients that key on submitted/cancelled close
// the card regardless of the underlying reason.
func PushTunnelAskUserResult(broker *tunnel.Broker, requestID string, update TunnelStateUpdate) {
	if broker == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	broker.PushAskUserResponse(requestID, tool.AskUserStatusCancelled, nil)
	ApplyTunnelStateUpdate(broker, update)
}

func ResolveTunnelApproval(decision string, toolName string, setOverride func(string)) permission.Decision {
	if (decision == tunnel.DecisionAlwaysAllow || decision == "always") && setOverride != nil && strings.TrimSpace(toolName) != "" {
		setOverride(toolName)
	}
	return ApprovalDecisionFromTunnel(decision)
}
