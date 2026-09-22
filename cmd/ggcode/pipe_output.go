package main

// Structured output for pipe mode, modeled on Claude Code headless mode's
// --output-format text|json|stream-json (the foundation of its Agent SDK and
// CI ecosystem): scripts and wrappers get a machine-readable contract instead
// of scraping human-oriented stdout.
//
//   - text (default): unchanged legacy behavior - raw assistant text stream.
//   - json: a single JSON object on the output writer after the run ends.
//   - stream-json: newline-delimited JSON events (system/assistant/tool_use/
//     tool_result/result), one object per line, for incremental consumers.
//
// The emitter is single-goroutine by construction: agent.RunStream invokes the
// stream callback sequentially, so no locking is needed. Write errors are
// sticky (first error wins) and surface through Finish's return value so the
// pipe exit code reflects a broken output artifact (#1444-B contract).

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	pipeFormatText       = "text"
	pipeFormatJSON       = "json"
	pipeFormatStreamJSON = "stream-json"
)

// Result subtypes, aligned with Claude Code's result event vocabulary.
const (
	pipeResultSuccess           = "success"
	pipeResultErrorExecution    = "error_during_execution"
	pipeResultErrorMaxOutTokens = "error_max_output_tokens"
)

// normalizePipeOutputFormat validates the --output-format flag value.
// An empty value (flag supplied without content) maps to text.
func normalizePipeOutputFormat(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", pipeFormatText:
		return pipeFormatText, nil
	case pipeFormatJSON:
		return pipeFormatJSON, nil
	case pipeFormatStreamJSON:
		return pipeFormatStreamJSON, nil
	default:
		return "", fmt.Errorf("invalid --output-format %q: must be one of text, json, stream-json", value)
	}
}

// pipeEmitterMeta is the static session descriptor reported in the stream-json
// init line and echoed on the result payload.
type pipeEmitterMeta struct {
	SessionID      string
	Model          string
	Cwd            string
	Tools          []string
	PermissionMode string
}

// pipeEmitter produces structured output for the json and stream-json formats.
// text mode never constructs one.
type pipeEmitter struct {
	w       io.Writer
	format  string
	meta    pipeEmitterMeta
	started time.Time

	textBuf   strings.Builder
	textDirty bool
	usage     provider.TokenUsage
	turns     int
	toolUses  int
	lastName  string
	truncated bool
	err       error
}

func newPipeEmitter(w io.Writer, format string, meta pipeEmitterMeta) *pipeEmitter {
	return &pipeEmitter{
		w:       w,
		format:  format,
		meta:    meta,
		started: time.Now(),
	}
}

// Init emits the stream-json system/init line. For json format it is a no-op.
func (e *pipeEmitter) Init() error {
	if e.format != pipeFormatStreamJSON {
		return nil
	}
	e.writeLine(pipeStreamInit{
		Type:           "system",
		Subtype:        "init",
		SessionID:      e.meta.SessionID,
		Model:          e.meta.Model,
		Cwd:            e.meta.Cwd,
		Tools:          e.meta.Tools,
		PermissionMode: e.meta.PermissionMode,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	})
	return e.err
}

// TextDelta buffers assistant text. In json mode it is the result payload; in
// stream-json mode it is flushed as an assistant message on message boundaries
// (before tool events and at finish), not per token.
func (e *pipeEmitter) TextDelta(text string) {
	if text == "" {
		return
	}
	e.textBuf.WriteString(text)
	e.textDirty = true
}

// TurnDone accumulates per-turn usage metadata from StreamEventDone events.
func (e *pipeEmitter) TurnDone(usage *provider.TokenUsage, truncated bool) {
	e.turns++
	if usage != nil {
		e.usage = e.usage.Add(*usage)
	}
	if truncated {
		e.truncated = true
	}
}

// ToolCallDone records a tool invocation. stream-json flushes the pending
// assistant message first so consumers see text-then-tool ordering.
func (e *pipeEmitter) ToolCallDone(name string, args json.RawMessage) {
	e.toolUses++
	e.lastName = name
	if e.format != pipeFormatStreamJSON {
		return
	}
	e.flushAssistant()
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	e.writeLine(pipeStreamToolUse{
		Type:      "tool_use",
		Name:      name,
		Input:     args,
		SessionID: e.meta.SessionID,
	})
}

// ToolResult records a tool outcome. The full result already flowed to the
// model; the event carries a bounded summary for progress observability so a
// huge read cannot flood the machine stream.
func (e *pipeEmitter) ToolResult(result string, isError bool) {
	if e.format != pipeFormatStreamJSON {
		return
	}
	e.writeLine(pipeStreamToolResult{
		Type:      "tool_result",
		Name:      e.lastName,
		Summary:   truncatePipeProgress(firstLine(result), 120),
		IsError:   isError,
		SessionID: e.meta.SessionID,
	})
}

// Finish flushes the final assistant message (stream-json) and the result
// payload. runErr is the agent-level error, if any; stream-level errors are
// folded in by the caller. Returns the sticky write error, if any.
func (e *pipeEmitter) Finish(runErr error) error {
	if e.format == pipeFormatStreamJSON {
		e.flushAssistant()
	}
	subtype := pipeResultSuccess
	errText := ""
	if e.truncated {
		subtype = pipeResultErrorMaxOutTokens
		errText = "output was cut off by the model's max output tokens limit"
	}
	if runErr != nil {
		subtype = pipeResultErrorExecution
		errText = runErr.Error()
	}
	e.writeLine(pipeResultPayload{
		Type:           "result",
		Subtype:        subtype,
		IsError:        subtype != pipeResultSuccess,
		DurationMs:     time.Since(e.started).Milliseconds(),
		Result:         e.textBuf.String(),
		Error:          errText,
		SessionID:      e.meta.SessionID,
		Model:          e.meta.Model,
		Cwd:            e.meta.Cwd,
		NumTurns:       e.turns,
		ToolUses:       e.toolUses,
		Usage:          e.usage,
		PermissionMode: e.meta.PermissionMode,
	})
	return e.err
}

func (e *pipeEmitter) flushAssistant() {
	if !e.textDirty {
		return
	}
	e.textDirty = false
	e.writeLine(pipeStreamAssistant{
		Type: "assistant",
		Message: pipeAssistantMessage{
			Role:    "assistant",
			Content: []pipeAssistantText{{Type: "text", Text: e.textBuf.String()}},
		},
		SessionID: e.meta.SessionID,
	})
}

func (e *pipeEmitter) writeLine(v any) {
	if e.err != nil {
		return // sticky: first write failure wins (#1444-B contract)
	}
	data, err := json.Marshal(v)
	if err != nil {
		e.err = fmt.Errorf("encoding %s output: %w", e.format, err)
		return
	}
	if _, err := fmt.Fprintf(e.w, "%s\n", data); err != nil {
		e.err = err
	}
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}

// pipeToolNames lists registered tool names for the init line.
func pipeToolNames(registry *tool.Registry) []string {
	defs := registry.ToDefinitions()
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return names
}

// --- wire types ---

type pipeStreamInit struct {
	Type           string   `json:"type"`
	Subtype        string   `json:"subtype"`
	SessionID      string   `json:"session_id"`
	Model          string   `json:"model,omitempty"`
	Cwd            string   `json:"cwd,omitempty"`
	Tools          []string `json:"tools"`
	PermissionMode string   `json:"permission_mode,omitempty"`
	Timestamp      string   `json:"timestamp"`
}

type pipeAssistantText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type pipeAssistantMessage struct {
	Role    string              `json:"role"`
	Content []pipeAssistantText `json:"content"`
}

type pipeStreamAssistant struct {
	Type      string               `json:"type"`
	Message   pipeAssistantMessage `json:"message"`
	SessionID string               `json:"session_id"`
}

type pipeStreamToolUse struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	SessionID string          `json:"session_id"`
}

type pipeStreamToolResult struct {
	Type      string `json:"type"`
	Name      string `json:"name,omitempty"`
	Summary   string `json:"summary,omitempty"`
	IsError   bool   `json:"is_error"`
	SessionID string `json:"session_id"`
}

type pipeResultPayload struct {
	Type           string              `json:"type"`
	Subtype        string              `json:"subtype"`
	IsError        bool                `json:"is_error"`
	DurationMs     int64               `json:"duration_ms"`
	Result         string              `json:"result"`
	Error          string              `json:"error,omitempty"`
	SessionID      string              `json:"session_id"`
	Model          string              `json:"model,omitempty"`
	Cwd            string              `json:"cwd,omitempty"`
	NumTurns       int                 `json:"num_turns"`
	ToolUses       int                 `json:"tool_uses"`
	Usage          provider.TokenUsage `json:"usage"`
	PermissionMode string              `json:"permission_mode,omitempty"`
}
