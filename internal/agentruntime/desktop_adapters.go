package agentruntime

import "github.com/topcheer/ggcode/internal/tunnel"

type DesktopMirrorCallbacks struct {
	CurrentBroker   func() *tunnel.Broker
	EnsureMessageID func(*tunnel.Broker) string
	ReasoningMsgID  func(*tunnel.Broker) string
	MarkMainActive  func()
	FlushMainStream func(*tunnel.Broker, bool)
}

type desktopMirrorAdapter struct {
	callbacks DesktopMirrorCallbacks
}

func NewDesktopMirrorAdapter(callbacks DesktopMirrorCallbacks) DesktopStreamMirror {
	return desktopMirrorAdapter{callbacks: callbacks}
}

func (m desktopMirrorAdapter) PushText(text string) {
	if m.callbacks.CurrentBroker == nil {
		return
	}
	if broker := m.callbacks.CurrentBroker(); broker != nil {
		if m.callbacks.MarkMainActive != nil {
			m.callbacks.MarkMainActive()
		}
		if m.callbacks.ReasoningMsgID != nil {
			broker.PushReasoningDone(m.callbacks.ReasoningMsgID(broker))
		}
		if m.callbacks.EnsureMessageID != nil {
			broker.PushText(m.callbacks.EnsureMessageID(broker), text)
		}
	}
}

func (m desktopMirrorAdapter) PushReasoning(chunk string) {
	if m.callbacks.CurrentBroker == nil {
		return
	}
	if broker := m.callbacks.CurrentBroker(); broker != nil {
		if m.callbacks.MarkMainActive != nil {
			m.callbacks.MarkMainActive()
		}
		if m.callbacks.ReasoningMsgID != nil {
			broker.PushReasoning(m.callbacks.ReasoningMsgID(broker), chunk)
		}
	}
}

func (m desktopMirrorAdapter) PushToolCall(toolID, toolName, displayName, rawArgs, detail string) {
	if m.callbacks.CurrentBroker == nil {
		return
	}
	if broker := m.callbacks.CurrentBroker(); broker != nil {
		broker.PushToolCall(toolID, toolName, displayName, rawArgs, detail)
	}
}

func (m desktopMirrorAdapter) PushToolResult(toolID, toolName, result string, isError bool) {
	if m.callbacks.CurrentBroker == nil {
		return
	}
	if broker := m.callbacks.CurrentBroker(); broker != nil {
		if m.callbacks.ReasoningMsgID != nil {
			broker.PushReasoningDone(m.callbacks.ReasoningMsgID(broker))
		}
		broker.PushToolResult(toolID, toolName, result, isError)
	}
}

func (m desktopMirrorAdapter) Flush(rotate bool) {
	if m.callbacks.CurrentBroker == nil || m.callbacks.FlushMainStream == nil {
		return
	}
	if broker := m.callbacks.CurrentBroker(); broker != nil {
		m.callbacks.FlushMainStream(broker, rotate)
	}
}

func (m desktopMirrorAdapter) PushError(message string) {
	if m.callbacks.CurrentBroker == nil {
		return
	}
	if broker := m.callbacks.CurrentBroker(); broker != nil {
		broker.PushError(message)
	}
}

type DesktopEmitterCallbacks struct {
	TriggerTypingFn    func()
	EmitToolResultFn   func(toolName, rawArgs, result string, isError bool)
	EmitRoundSummaryFn func(text string, toolCalls, toolSuccesses, toolFailures int)
}

type desktopEmitterAdapter struct {
	callbacks DesktopEmitterCallbacks
}

func NewDesktopEmitterAdapter(callbacks DesktopEmitterCallbacks) DesktopStreamEmitter {
	return desktopEmitterAdapter{callbacks: callbacks}
}

func (e desktopEmitterAdapter) TriggerTyping() {
	if e.callbacks.TriggerTypingFn != nil {
		e.callbacks.TriggerTypingFn()
	}
}

func (e desktopEmitterAdapter) EmitToolResult(toolName, rawArgs, result string, isError bool) {
	if e.callbacks.EmitToolResultFn != nil {
		e.callbacks.EmitToolResultFn(toolName, rawArgs, result, isError)
	}
}

func (e desktopEmitterAdapter) EmitRoundSummary(text string, toolCalls, toolSuccesses, toolFailures int) {
	if e.callbacks.EmitRoundSummaryFn != nil {
		e.callbacks.EmitRoundSummaryFn(text, toolCalls, toolSuccesses, toolFailures)
	}
}
