package swarm

// sa-90: step-level failure attribution for parked swarm tasks, the
// lightweight AgenTracer idea (arXiv 2509.03312 / ICLR 2026: attribute a
// multi-agent system failure to the specific agent AND step). Before this,
// a parked task carried only `permanent_error` + a 200-char error string -
// the board answered "it failed" but never "which tool call, on what target,
// at which step". Teammates already record a ToolCall/ToolResult event trail
// (maxTeammateEvents ring), so attribution is deterministic extraction from
// that trail at park time - no LLM, no counterfactual replay. Deliberately
// NOT reusing the main agent's causal_attribution.go: that state machine is
// wired into the primary agent loop and swarm teammates never pass through
// it (r121 cascade detection is task-level dependencies; this is step-level
// attribution inside one teammate's trail).

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// FailureAttribution is the step-level "who/when/what" record attached to a
// parked (permanently failed) board task. Serialized as JSON into the task
// metadata under AttributionMetaKey.
type FailureAttribution struct {
	AgentID     string    `json:"agent_id"`
	AgentName   string    `json:"agent_name,omitempty"`
	TaskID      string    `json:"task_id"`
	Step        int       `json:"step"`      // 1-based count of tool calls up to failure
	LastTool    string    `json:"last_tool"` // last tool invoked before the failure
	LastTarget  string    `json:"last_target,omitempty"`
	ErrorDigest string    `json:"error_digest"` // truncated classification+message
	ParkedAt    time.Time `json:"parked_at"`
}

// AttributionMetaKey is the task-board metadata key carrying the JSON blob.
const AttributionMetaKey = "attribution"

// attributionTargetKeys are the tool-argument fields consulted (first match
// wins) to name the object the failing step was operating on.
var attributionTargetKeys = []string{"file_path", "path", "command", "pattern", "query"}

// buildFailureAttribution extracts the step-level attribution from tm's
// event trail. Returns nil when the trail carries no tool events (pure
// stream failure before any call - e.g. auth rejected on the first token):
// attaching an empty record would be noise, the plain error string already
// says everything.
func buildFailureAttribution(tm *Teammate, taskID string, taskErr error) *FailureAttribution {
	if tm == nil || taskErr == nil {
		return nil
	}
	events, _ := tm.EventsSince(0)
	step, lastTool := 0, ""
	for _, ev := range events {
		if ev.Type == TeammateEventToolCall {
			step++
			lastTool = ev.ToolName
		}
	}
	if step == 0 || lastTool == "" {
		return nil
	}
	fa := &FailureAttribution{
		AgentID:     tm.ID,
		AgentName:   tm.Name,
		TaskID:      taskID,
		Step:        step,
		LastTool:    lastTool,
		ErrorDigest: util.Truncate(taskErr.Error(), 200),
		ParkedAt:    time.Now(),
	}
	// The tool ARGS live on the ToolCall event; scan backwards for the most
	// recent call of the same tool to recover its target.
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type == TeammateEventToolCall && ev.ToolName == lastTool {
			fa.LastTarget = util.Truncate(attributionTarget(ev.ToolArgs), 120)
			break
		}
	}
	return fa
}

// attributionTarget pulls the first recognized target field out of a JSON
// tool-args blob. Returns "" for non-JSON or unrecognized args.
func attributionTarget(toolArgs string) string {
	if toolArgs == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(toolArgs), &m); err != nil {
		return ""
	}
	for _, key := range attributionTargetKeys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// attributionJSON renders a FailureAttribution for task metadata, or "".
func attributionJSON(fa *FailureAttribution) string {
	if fa == nil {
		return ""
	}
	b, err := json.Marshal(fa)
	if err != nil {
		return ""
	}
	return string(b)
}

// attachAttribution sets the attribution metadata entry on an existing
// metadata map (mutating and returning it) so park sites can do:
//
//	meta := attachAttribution(meta, buildFailureAttribution(tm, id, err))
func attachAttribution(meta map[string]string, fa *FailureAttribution) map[string]string {
	if fa == nil {
		return meta
	}
	if meta == nil {
		meta = make(map[string]string, 2)
	}
	if blob := attributionJSON(fa); blob != "" {
		meta[AttributionMetaKey] = blob
	}
	return meta
}

// FormatAttribution renders the one-line human summary for task-board
// displays, or "" when the metadata carries no attribution.
func FormatAttribution(meta map[string]string) string {
	blob, ok := meta[AttributionMetaKey]
	if !ok || blob == "" {
		return ""
	}
	var fa FailureAttribution
	if err := json.Unmarshal([]byte(blob), &fa); err != nil {
		return ""
	}
	who := fa.AgentID
	if fa.AgentName != "" {
		who = fmt.Sprintf("%s(%s)", fa.AgentName, fa.AgentID)
	}
	target := ""
	if fa.LastTarget != "" {
		target = " " + fa.LastTarget
	}
	return fmt.Sprintf("[attribution] %s step#%d %s%s -> %s",
		who, fa.Step, fa.LastTool, target, fa.ErrorDigest)
}
