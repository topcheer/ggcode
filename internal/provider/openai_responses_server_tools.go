package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

func responsesHostedTool(t ServerToolConfig) (responsesTool, bool) {
	normalized := strings.ToLower(strings.TrimSpace(t.Type))
	switch normalized {

	case "apply_patch":
		return responsesTool{Type: normalized}, true
	case "web_search", "web_search_2025_08_26", "web_search_preview":
		return responsesTool{Type: normalized}, true
	default:
		debug.Log("openai-responses", "ignoring unsupported server tool declaration: %q", t.Type)
		return responsesTool{}, false
	}
}

func responsesServerToolBlock(raw json.RawMessage) ContentBlock {
	b := ContentBlock{
		Type:       "server_tool",
		ServerTool: "web_search",
		Raw:        append(json.RawMessage(nil), raw...),
	}
	var probe struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &probe) == nil {
		b.ID = probe.ID
	}
	return b
}

func decodeResponsesServerToolItem(raw json.RawMessage) (responsesInputItem, bool) {
	if len(raw) == 0 {
		return responsesInputItem{}, false
	}
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return responsesInputItem{}, false
	}

	if strings.HasPrefix(probe.Type, "web_search") && strings.HasSuffix(probe.Type, "_call") {
		return responsesInputItem{RawReplay: raw}, true
	}
	return responsesInputItem{}, false
}

const applyPatchInternalToolName = "apply_patch"

func decodeResponsesApplyPatchCall(raw json.RawMessage) (callID string, operation json.RawMessage, ok bool) {
	if len(raw) == 0 {
		return "", nil, false
	}
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.Type != "apply_patch_call" {
		return "", nil, false
	}
	var op struct {
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Operation json.RawMessage `json:"operation"`
	}
	if json.Unmarshal(raw, &op) != nil {
		return "", nil, false
	}
	callID = op.CallID
	if callID == "" {
		callID = op.ID
	}
	if callID == "" {
		return "", nil, false
	}
	return callID, op.Operation, true
}

func responsesApplyPatchCallReplay(callID string, operation json.RawMessage) json.RawMessage {
	id, _ := json.Marshal(callID)
	op := "null"
	if len(operation) > 0 && json.Valid(operation) {
		op = string(operation)
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"apply_patch_call","call_id":%s,"status":"completed","operation":%s}`, id, op))
}

func responsesApplyPatchOutputReplay(callID, output string, failed bool) json.RawMessage {
	id, _ := json.Marshal(callID)
	out, _ := json.Marshal(output)
	status := "completed"
	if failed {
		status = "failed"
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"apply_patch_call_output","call_id":%s,"status":%q,"output":%s}`, id, status, out))
}
