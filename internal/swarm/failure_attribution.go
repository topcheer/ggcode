package swarm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

type FailureAttribution struct {
	AgentID     string    `json:"agent_id"`
	AgentName   string    `json:"agent_name,omitempty"`
	TaskID      string    `json:"task_id"`
	Step        int       `json:"step"`
	LastTool    string    `json:"last_tool"`
	LastTarget  string    `json:"last_target,omitempty"`
	ErrorDigest string    `json:"error_digest"`
	ParkedAt    time.Time `json:"parked_at"`
}

const AttributionMetaKey = "attribution"

var attributionTargetKeys = []string{"file_path", "path", "command", "pattern", "query"}

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

	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type == TeammateEventToolCall && ev.ToolName == lastTool {
			fa.LastTarget = util.Truncate(attributionTarget(ev.ToolArgs), 120)
			break
		}
	}
	return fa
}

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
