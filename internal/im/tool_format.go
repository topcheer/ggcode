package im

import (
	"fmt"
	"strings"
)

func (a *tgAdapter) outboundText(event OutboundEvent) string {
	return defaultOutboundText(event)
}

// toolLang returns the ToolLanguage from a struct's Lang field, defaulting to zh-CN.
func toolLang(lang string) ToolLanguage {
	if lang == "en" {
		return ToolLangEn
	}
	return ToolLangZhCN
}

// formatToolResultText formats a tool result event into concise IM text,
// mirroring the terminal follow display style: icon + tool name + brief summary.
// Returns empty string if the tool result should be silently suppressed (e.g. read_file success).
func formatToolResultText(tr *ToolResultInfo) string {
	// Special formatting for certain tool types.
	// handled is true when formatSpecialIMToolResult has handled this tool
	// (including the "suppress" case like read_file success).
	handled, special := formatSpecialIMToolResult(tr)
	if handled {
		// When a tool-call start notification was already sent, strip the
		// tool name header from the result to avoid duplicate IM messages.
		if tr.CallNotified && special != "" {
			special = stripToolHeader(tr.ToolName, special)
		}
		return special
	}

	// Default: prettified tool name
	pretty := prettifyToolName(tr.ToolName)
	output := strings.TrimSpace(redactResult(tr.Result))
	if output != "" {
		if tr.CallNotified {
			// Only show the result output, no tool name header
			return imCodeBlock(output)
		}
		return fmt.Sprintf("🔧 %s\n%s", pretty, imCodeBlock(output))
	}
	return fmt.Sprintf("🔧 %s", pretty)
}

// stripToolHeader removes the tool name/icon prefix from a formatted result
// string when the tool-call start notification was already sent to IM.
// For example, "⚡ Run command\n```\noutput\n```" becomes "```\noutput\n```".
func stripToolHeader(toolName, formatted string) string {
	// If the result starts with an icon + label on the first line,
	// strip everything up to the first newline (or code block start).
	lines := strings.SplitN(formatted, "\n", 2)
	if len(lines) < 2 {
		return formatted // single-line result, keep as-is
	}
	rest := strings.TrimSpace(lines[1])
	if rest == "" {
		return formatted
	}
	return rest
}

func (a *WechatAdapter) outboundText(event OutboundEvent) string {
	return defaultOutboundText(event)
}

func (a *dingtalkAdapter) outboundText(event OutboundEvent) string {
	return defaultOutboundText(event)
}

// defaultOutboundText is the shared outboundText implementation used by adapters
// that do not need custom per-adapter formatting.
func defaultOutboundText(event OutboundEvent) string {
	switch event.Kind {
	case OutboundEventText:
		return event.Text
	case OutboundEventStatus:
		return event.Status
	case OutboundEventToolCall:
		if event.ToolCall == nil {
			return ""
		}
		return formatToolCallText(event.ToolCall)
	case OutboundEventToolResult:
		if event.ToolRes == nil {
			return ""
		}
		return formatToolResultText(event.ToolRes)
	case OutboundEventApprovalRequest:
		if event.Approval == nil {
			return ""
		}
		return fmt.Sprintf("[approval] %s\n%s", event.Approval.ToolName, redactResult(event.Approval.Input))
	case OutboundEventApprovalResult:
		if event.Result == nil {
			return ""
		}
		return fmt.Sprintf("[approval result] %s", event.Result.Decision)
	default:
		return ""
	}
}
