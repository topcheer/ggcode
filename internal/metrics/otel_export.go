package metrics

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func ExportTraceOTLP(sessionID, vendor, endpoint, model string, createdAt time.Time, events []MetricEvent) ([]byte, error) {
	tree := BuildSpanTree(sessionID, events)
	traceID := otlpTraceID(sessionID)
	system := genAISystem(vendor)

	flat := FlattenSpans(tree)
	spans := make([]map[string]any, 0, len(flat))
	for _, sn := range flat {
		spans = append(spans, otlpSpan(sn, traceID, sessionID, system))
	}

	doc := map[string]any{
		"resourceSpans": []any{
			map[string]any{
				"resource": map[string]any{
					"attributes": []any{otlpAttr("service.name", "ggcode")},
				},
				"scopeSpans": []any{
					map[string]any{
						"scope": map[string]any{
							"name": "github.com/topcheer/ggcode/internal/metrics",
						},
						"spans": spans,
					},
				},
			},
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

func otlpTraceID(sessionID string) string {
	h := sha1.Sum([]byte(sessionID))
	return hex.EncodeToString(h[:16])
}

func genAISystem(vendor string) string {
	v := strings.ToLower(strings.TrimSpace(vendor))
	switch v {
	case "google":
		return "gemini"
	default:
		return v
	}
}

func otlpSpan(sn SpanNode, traceID, sessionID, system string) map[string]any {
	span := map[string]any{
		"traceId":           traceID,
		"spanId":            sn.SpanID,
		"name":              sn.Name,
		"kind":              "SPAN_KIND_INTERNAL",
		"startTimeUnixNano": otlpNano(sn.StartTime),
		"endTimeUnixNano":   otlpNano(sn.EndTime),
	}
	if sn.ParentID != "" {
		span["parentSpanId"] = sn.ParentID
	}

	attrs := []any{}
	switch sn.Kind {
	case "session":
		span["name"] = "agent.run"
		attrs = append(attrs,
			otlpAttr("gen_ai.agent.name", "ggcode"),
			otlpAttr("session.id", sessionID),
		)
	case "turn":
		attrs = append(attrs, otlpAttr("session.id", sessionID))
	case "llm":
		span["kind"] = "SPAN_KIND_CLIENT"
		model, _ := sn.Attributes["model"].(string)
		if model != "" {
			span["name"] = "chat " + model
		} else {
			span["name"] = "chat"
		}
		attrs = append(attrs,
			otlpAttr("gen_ai.operation.name", "chat"),
			otlpAttr("gen_ai.conversation.id", sessionID),
		)

		if agentID, _ := sn.Attributes["agent_id"].(string); agentID != "" {
			attrs = append(attrs, otlpAttr("gen_ai.agent.name", "ggcode:subagent:"+agentID))
		}
		if system != "" {
			attrs = append(attrs, otlpAttr("gen_ai.system", system))
		}
		if model != "" {
			attrs = append(attrs,
				otlpAttr("gen_ai.request.model", model),
				otlpAttr("gen_ai.response.model", model),
			)
		}
		if v, ok := sn.Attributes["input_tokens"].(int); ok && v > 0 {
			attrs = append(attrs, otlpAttr("gen_ai.usage.input_tokens", v))
		}
		if v, ok := sn.Attributes["output_tokens"].(int); ok && v > 0 {
			attrs = append(attrs, otlpAttr("gen_ai.usage.output_tokens", v))
		}
		for _, k := range []string{"ttft_ms", "think_time_ms", "cache_read_tokens", "cache_write_tokens"} {
			if v, ok := sn.Attributes[k].(int); ok && v > 0 {
				attrs = append(attrs, otlpAttr("ggcode."+k, v))
			}
		}
	case "tool":
		toolName, _ := sn.Attributes["tool_name"].(string)
		if toolName == "" {
			toolName = "unknown"
		}
		span["name"] = "execute_tool " + toolName
		attrs = append(attrs,
			otlpAttr("gen_ai.operation.name", "execute_tool"),
			otlpAttr("gen_ai.tool.name", toolName),
		)
		if agentID, _ := sn.Attributes["agent_id"].(string); agentID != "" {
			attrs = append(attrs, otlpAttr("gen_ai.agent.name", "ggcode:subagent:"+agentID))
		}
		success, _ := sn.Attributes["success"].(bool)
		errMsg, _ := sn.Attributes["error"].(string)
		if !success || errMsg != "" {
			msg := errMsg
			if msg == "" {
				msg = "tool failed"
			}
			attrs = append(attrs, otlpAttr("error.type", "tool_failure"))
			span["status"] = map[string]any{"code": "STATUS_CODE_ERROR", "message": msg}
		}
	default:

		for k, v := range sn.Attributes {
			attrs = append(attrs, otlpAttr("ggcode."+k, v))
		}
	}

	sort.Slice(attrs, func(i, j int) bool {
		return attrs[i].(map[string]any)["key"].(string) <
			attrs[j].(map[string]any)["key"].(string)
	})
	span["attributes"] = attrs
	return span
}

func otlpNano(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return strconv.FormatInt(t.UnixNano(), 10)
}

func otlpAttr(key string, value any) map[string]any {
	var val map[string]any
	switch v := value.(type) {
	case string:
		val = map[string]any{"stringValue": v}
	case int:
		val = map[string]any{"intValue": strconv.Itoa(v)}
	case int64:
		val = map[string]any{"intValue": strconv.FormatInt(v, 10)}
	case bool:
		val = map[string]any{"boolValue": v}
	case float64:
		val = map[string]any{"doubleValue": v}
	default:
		val = map[string]any{"stringValue": fmt.Sprint(value)}
	}
	return map[string]any{"key": key, "value": val}
}
