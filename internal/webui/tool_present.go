package webui

import (
	"encoding/json"
	"strings"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// Tool presentation for the webui: the browser must never render a raw
// tool name or raw JSON as the primary surface. The display fields are
// computed here with the same shared presentation layer the TUI, IM, desktop
// and mobile tunnel use (tool.DescribeTool / tool.DescribeToolResult), so a
// tool call reads the same on every surface.

type toolDisplay struct {
	DisplayName string
	Detail      string
	Summary     string
	Payload     string
	PayloadMode string
}

// toolCallDisplay is the display form of a tool call (name + one-line
// target). rawArgs may be empty or partial (streaming chunks).
func toolCallDisplay(toolName, rawArgs string) toolDisplay {
	pres := toolpkg.DescribeTool(toolName, rawArgs)
	name := strings.TrimSpace(pres.DisplayName)
	if name == "" {
		name = toolName
	}
	detail := strings.TrimSpace(pres.Detail)
	// Command tools: the shared layer uses the command (or its description)
	// as the title and leaves detail empty; the browser shows the title in
	// the header and the full command as the detail line, so repeat a
	// compact form of the command when there is no description to pair with.
	if detail == "" && isCommandTool(toolName) {
		detail = compactFirstLine(commandArg(rawArgs))
	}
	return toolDisplay{DisplayName: name, Detail: detail}
}

func isCommandTool(toolName string) bool {
	switch toolName {
	case "run_command", "start_command", "bash", "powershell":
		return true
	}
	return false
}

func commandArg(rawArgs string) string {
	var args struct {
		Command string `json:"command"`
		Cmd     string `json:"cmd"`
	}
	if json.Unmarshal([]byte(rawArgs), &args) != nil {
		return ""
	}
	if args.Command != "" {
		return args.Command
	}
	return args.Cmd
}

// toolResultDisplay adds the result presentation: a short summary (shown in
// the collapsed card header) and an optional payload the browser renders by
// mode (text / json / task_list / cron_list ...). Falls back to the raw
// result as text, exactly like the tunnel does for mobile.
func toolResultDisplay(toolName, rawArgs, result string, isError bool) toolDisplay {
	d := toolCallDisplay(toolName, rawArgs)
	if strings.TrimSpace(result) == "" {
		return d
	}
	if pres, ok := toolpkg.DescribeToolResult(toolName, rawArgs, result, isError); ok {
		d.Summary = pres.Summary
		d.Payload = pres.Payload
		d.PayloadMode = pres.PayloadMode
		return d
	}
	// No designed summary: leave summary empty so the browser derives the
	// badge from the kind (line count / match count) instead of echoing the
	// first line of the content.
	d.Payload = result
	d.PayloadMode = "text"
	return d
}

// nameAndDetail is the display pair for a tool call.
func (d toolDisplay) nameAndDetail() (string, string) { return d.DisplayName, d.Detail }

// resultFields is the full display tuple for a tool result.
func (d toolDisplay) resultFields() (name, detail, summary, payload, mode string) {
	return d.DisplayName, d.Detail, d.Summary, d.Payload, d.PayloadMode
}

// stripSpotlightWrapper removes the model-facing prompt-injection fence
// (<untrusted_tool_output source="x"> ... UNTRUSTED DATA ... </...>) that
// the agent puts around persisted tool results. It is a fence for the LLM,
// not for the human reading the webui; the live stream never carries it.
var (
	spotlightOpen  = "<untrusted_tool_output source=\""
	spotlightClose = "</untrusted_tool_output>"
)

func stripSpotlightWrapper(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, spotlightOpen) {
		return s
	}
	body, ok := strings.CutPrefix(t, spotlightOpen)
	if !ok {
		return s
	}
	// drop the source attribute and the banner line
	if i := strings.Index(body, ">\n"); i >= 0 {
		body = body[i+2:]
	} else if i := strings.Index(body, ">"); i >= 0 {
		body = body[i+1:]
	}
	body = strings.TrimSuffix(strings.TrimRight(body, "\n"), spotlightClose)
	if i := strings.Index(body, "\n"); i >= 0 && strings.HasPrefix(body, "UNTRUSTED DATA:") {
		body = body[i+1:]
	}
	return strings.TrimSpace(body)
}

// summaryLineCap bounds the collapsed one-line summary of an unparsed result.
const summaryLineCap = 120

// compactFirstLine is the generic one-line summary of an unparsed result.
func compactFirstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	runes := []rune(s)
	if len(runes) > summaryLineCap {
		return string(runes[:summaryLineCap]) + "..."
	}
	return s
}
