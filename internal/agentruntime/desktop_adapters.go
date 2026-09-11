package agentruntime

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
