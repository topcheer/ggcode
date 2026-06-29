package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

const (
	desktopToolResultLimit  = 2000
	desktopToolPreviewLimit = 500
)

type DesktopStreamMirror interface {
	PushText(text string)
	PushReasoning(chunk string)
	PushToolCall(toolID, toolName, displayName, rawArgs, detail string)
	PushToolResult(toolID, toolName, result string, isError bool)
	Flush(rotate bool)
	PushError(message string)
}

type DesktopStreamEmitter interface {
	TriggerTyping()
	EmitToolResult(toolName, rawArgs, result string, isError bool)
	EmitRoundSummary(text string, toolCalls, toolSuccesses, toolFailures int)
}

type DesktopToolCallEvent struct {
	ID          string
	Name        string
	RawArgs     string
	DisplayName string
	Detail      string
	Inline      string
}

type DesktopToolResultEvent struct {
	ID      string
	Name    string
	RawArgs string
	Content string
	Preview string
	IsError bool
}

type DesktopStreamSemantic struct {
	Type       provider.StreamEventType
	Text       string
	ToolCall   *DesktopToolCallEvent
	ToolResult *DesktopToolResultEvent
	UsageData  map[string]interface{}
	ErrorText  string
}

func HandleDesktopStreamEvent(ev provider.StreamEvent, round *IMRoundState, emitter DesktopStreamEmitter, mirror DesktopStreamMirror) (DesktopStreamSemantic, bool) {
	switch ev.Type {
	case provider.StreamEventText:
		round.AppendText(ev.Text)
		if mirror != nil {
			mirror.PushText(ev.Text)
		}
		return DesktopStreamSemantic{Type: ev.Type, Text: ev.Text}, true

	case provider.StreamEventToolCallDone:
		name := ev.Tool.Name
		if name == "" {
			name = "tool"
		}
		rawArgs := string(ev.Tool.Arguments)
		present := toolpkg.DescribeTool(name, rawArgs)
		inline := toolpkg.FormatToolInline(present.DisplayName, present.Detail)
		round.NoteToolCall()
		if emitter != nil {
			emitter.TriggerTyping()
		}
		if mirror != nil {
			mirror.Flush(true)
			mirror.PushToolCall(ev.Tool.ID, name, present.DisplayName, rawArgs, present.Detail)
		}
		return DesktopStreamSemantic{
			Type: ev.Type,
			ToolCall: &DesktopToolCallEvent{
				ID:          ev.Tool.ID,
				Name:        name,
				RawArgs:     rawArgs,
				DisplayName: present.DisplayName,
				Detail:      present.Detail,
				Inline:      inline,
			},
		}, true

	case provider.StreamEventToolResult:
		resultText := ev.Result
		rawArgs := string(ev.Tool.Arguments)
		// Format lanchat list results into a human-readable list
		// instead of raw JSON.
		if pres, ok := toolpkg.DescribeToolResult(ev.Tool.Name, rawArgs, resultText, ev.IsError); ok && pres.Payload != "" {
			resultText = pres.Payload
		}
		content := truncateDesktopToolResult(resultText)
		preview := previewDesktopToolResult(resultText)
		round.NoteToolResult(ev.IsError)
		if emitter != nil {
			emitter.EmitToolResult(ev.Tool.Name, rawArgs, content, ev.IsError)
			emitter.TriggerTyping()
		}
		if mirror != nil {
			mirror.PushToolResult(ev.Tool.ID, ev.Tool.Name, content, ev.IsError)
		}
		return DesktopStreamSemantic{
			Type: ev.Type,
			ToolResult: &DesktopToolResultEvent{
				ID:      ev.Tool.ID,
				Name:    ev.Tool.Name,
				RawArgs: rawArgs,
				Content: content,
				Preview: preview,
				IsError: ev.IsError,
			},
		}, true

	case provider.StreamEventReasoning:
		chunk := tunnel.NormalizeReasoningChunk(ev.Text)
		if chunk == "" {
			return DesktopStreamSemantic{}, false
		}
		if mirror != nil {
			mirror.PushReasoning(chunk)
		}
		return DesktopStreamSemantic{Type: ev.Type, Text: chunk}, true

	case provider.StreamEventDone:
		if emitter != nil {
			text := strings.TrimSpace(round.Text())
			if text != "" || round.ToolCalls > 0 {
				emitter.EmitRoundSummary(text, round.ToolCalls, round.ToolSuccesses, round.ToolFailures)
			}
		}
		round.Reset()
		if mirror != nil {
			mirror.Flush(true)
		}
		return DesktopStreamSemantic{
			Type:      ev.Type,
			UsageData: desktopUsageData(ev.Usage),
		}, true

	case provider.StreamEventError:
		message := "unknown error"
		if ev.Error != nil {
			message = ev.Error.Error()
		}
		if mirror != nil {
			mirror.Flush(true)
			mirror.PushError(message)
		}
		return DesktopStreamSemantic{Type: ev.Type, ErrorText: message}, true
	}
	return DesktopStreamSemantic{}, false
}

func truncateDesktopToolResult(result string) string {
	return truncateRunes(result, desktopToolResultLimit, "\n...(truncated)")
}

func previewDesktopToolResult(result string) string {
	return truncateRunes(result, desktopToolPreviewLimit, "...")
}

func desktopUsageData(usage *provider.TokenUsage) map[string]interface{} {
	if usage == nil {
		return map[string]interface{}{}
	}
	cacheTotal := usage.CacheRead + usage.CacheWrite + usage.InputTokens
	cacheHit := 0
	if cacheTotal > 0 && usage.CacheRead > 0 {
		cacheHit = usage.CacheRead * 100 / cacheTotal
	}
	return map[string]interface{}{
		"inputTokens":  usage.InputTokens + usage.CacheRead,
		"outputTokens": usage.OutputTokens,
		"cacheRead":    usage.CacheRead,
		"cacheWrite":   usage.CacheWrite,
		"cacheHit":     cacheHit,
	}
}

func truncateRunes(s string, maxRunes int, suffix string) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if suffix == "" {
		return string(runes[:maxRunes])
	}
	suffixRunes := []rune(suffix)
	limit := maxRunes - len(suffixRunes)
	if limit <= 0 {
		return string(suffixRunes[:maxRunes])
	}
	return string(runes[:limit]) + suffix
}
