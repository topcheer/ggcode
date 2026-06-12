package agentruntime

import "github.com/topcheer/ggcode/internal/tunnel"

type TunnelMainStream struct {
	MessageID     string
	NeedsFinalize bool
}

func EnsureTunnelMainStream(state TunnelMainStream, broker *tunnel.Broker) TunnelMainStream {
	if state.MessageID == "" && broker != nil {
		state.MessageID = broker.NextMessageID()
	}
	return state
}

func TunnelReasoningMsgID(messageID string) string {
	if messageID == "" {
		return ""
	}
	return messageID + "-reasoning"
}

func MarkTunnelMainStreamActive(state TunnelMainStream) TunnelMainStream {
	if state.MessageID != "" {
		state.NeedsFinalize = true
	}
	return state
}

func FlushTunnelMainStream(state TunnelMainStream, broker *tunnel.Broker, force bool) TunnelMainStream {
	if broker == nil || state.MessageID == "" {
		return state
	}
	if !force && !state.NeedsFinalize {
		return state
	}
	broker.PushReasoningDone(TunnelReasoningMsgID(state.MessageID))
	broker.PushTextDone(state.MessageID)
	state.MessageID = broker.NextMessageID()
	state.NeedsFinalize = false
	return state
}
