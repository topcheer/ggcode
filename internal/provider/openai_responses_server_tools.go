package provider

// OpenAI Responses hosted (server-side) tools (sa-62).
//
// The Responses API ships hosted tools that execute inside OpenAI's
// infrastructure - {type: "web_search"} being the flagship. Like Anthropic's
// server_tool_use and Gemini's google_search/url_context built-ins, ggcode
// never receives an executable client-side tool_use for them: the model calls
// the tool in-API and the response output carries web_search_call items
// describing what was searched. This leg closes the provider symmetry: all
// three major protocols now accept the same config declaration
// (`server_tools: [{type: ...}]`) via ServerToolsSetter.
//
// Stateless replay contract: ggcode always sends store=false, so server-side
// conversation state is discarded between turns. A web_search_call item from
// a previous response must therefore be echoed back verbatim in the next
// request's input or the model loses the search context entirely. Items ride
// the shared "server_tool" content-block carrier (Raw = verbatim JSON) and
// buildResponsesInput replays them byte-for-byte. Foreign provider blocks
// (Anthropic/Gemini signatures from a failed-over session) fail the type
// probe and are dropped, keeping cross-provider failover leak-free.
//
// References:
//   - https://developers.openai.com/api/docs/guides/tools-web-search
//   - https://github.com/openai/openai-go (responses.WebSearchTool params)

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// responsesHostedTool renders one configured server tool as a Responses API
// hosted-tool entry. Hosted tools carry no name/parameters - they are
// addressed purely by type. Unknown types are rejected (fail closed).
func responsesHostedTool(t ServerToolConfig) (responsesTool, bool) {
	normalized := strings.ToLower(strings.TrimSpace(t.Type))
	switch normalized {
	// sa-67: apply_patch is declared in-API (the model addresses it purely by
	// type) but executed CLIENT-side - see applyPatchInternalToolName.
	case "apply_patch":
		return responsesTool{Type: normalized}, true
	case "web_search", "web_search_2025_08_26", "web_search_preview":
		return responsesTool{Type: normalized}, true
	default:
		debug.Log("openai-responses", "ignoring unsupported server tool declaration: %q", t.Type)
		return responsesTool{}, false
	}
}

// responsesServerToolBlock wraps a raw hosted-tool output item (e.g.
// web_search_call) into the shared "server_tool" carrier block. The verbatim
// JSON rides in Raw so buildResponsesInput can echo it back losslessly under
// store=false.
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

// decodeResponsesServerToolItem parses a raw hosted-tool item captured from a
// previous Responses response (stored in a "server_tool" block's Raw) and
// reports whether it is replayable. Only hosted-tool call items are echoed;
// anything else (foreign provider signatures, malformed JSON) is dropped.
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
	// "web_search_call" today; the suffix match tolerates variant/legacy
	// naming (e.g. preview relays) without opening the door to message or
	// reasoning items. apply_patch_call is NOT a hosted tool (sa-67): it is
	// handled by the dedicated apply_patch path, never as a server_tool.
	if strings.HasPrefix(probe.Type, "web_search") && strings.HasSuffix(probe.Type, "_call") {
		return responsesInputItem{RawReplay: raw}, true
	}
	return responsesInputItem{}, false
}

// applyPatchInternalToolName is the hidden internal tool that executes V4A
// patches client-side (internal/tool.ApplyPatch). The Responses provider
// surfaces each apply_patch_call output item as a call to this name, and maps
// the executor's Result back into apply_patch_call_output. It must match the
// tool's Name() exactly; the const lives here because provider cannot import
// internal/tool (tool imports provider for its definitions).
const applyPatchInternalToolName = "apply_patch"

// decodeResponsesApplyPatchCall probes a raw output item (non-stream output
// array entry or response.output_item.* event payload) for an apply_patch_call
// item and extracts the call id plus the operation object. Returns ok=false
// for anything else - foreign providers, function_call, web_search_call,
// malformed JSON.
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

// responsesApplyPatchCallReplay rebuilds the model's apply_patch_call item for
// stateless replay (store=false discards server state, so the next request
// must echo the call back). The operation rides through verbatim when it is
// valid JSON; corrupt inputs replay without an operation so the item still
// pairs with its output and the model sees its call was attempted.
func responsesApplyPatchCallReplay(callID string, operation json.RawMessage) json.RawMessage {
	id, _ := json.Marshal(callID)
	op := "null"
	if len(operation) > 0 && json.Valid(operation) {
		op = string(operation)
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"apply_patch_call","call_id":%s,"status":"completed","operation":%s}`, id, op))
}

// responsesApplyPatchOutputReplay builds the apply_patch_call_output item
// reporting the local executor's outcome. status mirrors the Result error
// flag exactly like anthropic.NewToolResultBlock does for tool_result.
func responsesApplyPatchOutputReplay(callID, output string, failed bool) json.RawMessage {
	id, _ := json.Marshal(callID)
	out, _ := json.Marshal(output)
	status := "completed"
	if failed {
		status = "failed"
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"apply_patch_call_output","call_id":%s,"status":%q,"output":%s}`, id, status, out))
}
