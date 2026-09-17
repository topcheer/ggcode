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
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// SetServerTools implements provider.ServerToolsSetter: declarative hosted
// tools executed inside the OpenAI Responses API (never client-side).
// Mapping:
//   - "web_search"            → GA hosted web search ({type:"web_search"})
//   - "web_search_2025_08_26" → dated GA snapshot of the same tool
//   - "web_search_preview"    → legacy preview alias (kept for older relays)
//
// Unknown declarations are ignored (fail closed), mirroring the Anthropic and
// Gemini legs; a declaration set with no recognized entry leaves the
// previously stored set untouched so config reloads cannot wipe it.
func (p *OpenAIResponsesProvider) SetServerTools(tools []ServerToolConfig) {
	kept := make([]ServerToolConfig, 0, len(tools))
	for _, t := range tools {
		if _, ok := responsesHostedTool(t); ok {
			kept = append(kept, t)
		}
	}
	if len(kept) > 0 {
		p.serverTools = kept
	}
}

// hasHostedTools reports whether any hosted (server-side) tool is configured.
func (p *OpenAIResponsesProvider) hasHostedTools() bool { return len(p.serverTools) > 0 }

// responsesHostedTool renders one configured server tool as a Responses API
// hosted-tool entry. Hosted tools carry no name/parameters - they are
// addressed purely by type. Unknown types are rejected (fail closed).
func responsesHostedTool(t ServerToolConfig) (responsesTool, bool) {
	normalized := strings.ToLower(strings.TrimSpace(t.Type))
	switch normalized {
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
	// reasoning items.
	if strings.HasPrefix(probe.Type, "web_search") && strings.HasSuffix(probe.Type, "_call") {
		return responsesInputItem{RawReplay: raw}, true
	}
	return responsesInputItem{}, false
}
