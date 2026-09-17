package provider

// OpenAI Responses API adapter (sa-40).
//
// The Responses API is OpenAI's flagship agent-facing surface (2025-2026):
// Codex-family and o-series models are served through /v1/responses, and the
// protocol differs materially from Chat Completions (stateful `input` item
// stream, function_call/function_call_output items, output_text.delta SSE
// events). Until now ggcode's openai protocol only ever spoke
// /chat/completions, so those models were unreachable.
//
// Activation paths (internal/provider/registry.go):
//   - protocol "openai-responses" in the endpoint config, or
//   - protocol "openai" with a BaseURL whose path ends in "/responses".
//
// Scope note: this adapter is stateless per call - it always sends the full
// conversation as `input` items instead of using `previous_response_id`.
// Stateless replay keeps ggcode's existing context-manager/failover/compaction
// machinery working unchanged; the server-side state remains an optimization
// for a later pass.
//
// Encrypted reasoning round-trip (sa-54): because every request runs with
// store=false, OpenAI reasoning models (o-series, gpt-5.x, codex) lose their
// chain-of-thought between tool calls unless the client replays the reasoning
// items back. We request `include: ["reasoning.encrypted_content"]`, capture
// the opaque reasoning items the API returns, and echo them verbatim at the
// head of the assistant turn on the next request - exactly what OpenAI's
// stateless-multi-turn guidance requires. Items without encrypted content are
// not replayed (nothing useful to reconstruct, and item ids alone cannot be
// resolved once store=false discards server state).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAIResponsesProvider speaks the /v1/responses protocol.
type OpenAIResponsesProvider struct {
	apiKey     string
	model      string
	baseURL    string
	maxTokens  int
	httpClient *http.Client

	reasoningEffort   string
	toolChoice        string
	maxTokensOverride int

	// Hosted (server-side) tools configured via server_tools (sa-62), e.g.
	// web_search. Declared once in config and executed inside the API.
	serverTools []ServerToolConfig
}

// NewOpenAIResponsesProvider creates a provider for an OpenAI-compatible
// /v1/responses endpoint. baseURL should include the /v1 segment; the
// "/responses" suffix is appended if missing.
func NewOpenAIResponsesProvider(apiKey, model string, maxTokens int, baseURL string) *OpenAIResponsesProvider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasSuffix(baseURL, "/responses") {
		baseURL += "/responses"
	}
	return &OpenAIResponsesProvider{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		maxTokens:  maxTokens,
		httpClient: &http.Client{Timeout: 0}, // streams are long-lived; ctx governs lifetime
	}
}

func (p *OpenAIResponsesProvider) Name() string { return "openai-responses" }

// SetReasoningEffort stores the reasoning effort level for the request's
// `reasoning.effort` field (mirrors the openai chat adapter).
func (p *OpenAIResponsesProvider) SetReasoningEffort(effort string) {
	p.reasoningEffort = normalizeResponsesEffort(effort)
}

func (p *OpenAIResponsesProvider) ReasoningEffort() string { return p.reasoningEffort }

// SetToolChoice stores tool_choice ("auto", "required", "none" or a JSON
// object). Empty means the API default ("auto").
func (p *OpenAIResponsesProvider) SetToolChoice(choice string) {
	p.toolChoice = strings.TrimSpace(choice)
}

func (p *OpenAIResponsesProvider) ToolChoice() string { return p.toolChoice }

// SetMaxTokens honors the MaxTokensSetter contract (MCP sampling #1592-A).
func (p *OpenAIResponsesProvider) SetMaxTokens(n int) { p.maxTokensOverride = n }

func (p *OpenAIResponsesProvider) CountTokens(ctx context.Context, messages []Message) (int, error) {
	return estimateTokensForMessages(messages), nil
}

type sseEventHandler struct{}

// normalizeResponsesEffort maps the shared effort vocabulary (minimal/low/
// medium/high/xhigh/max/turbo) onto the Responses API values. Unknown levels
// are passed through untouched so future API levels keep working.
func normalizeResponsesEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "":
		return ""
	case "turbo", "xhigh", "max":
		return "high"
	case "minimal":
		return "low"
	default:
		return strings.ToLower(strings.TrimSpace(effort))
	}
}

// ---- request types ----

type responsesInputItem struct {
	Type    string          `json:"type,omitempty"` // "message", "function_call", "function_call_output", "reasoning"
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"` // string or part array, per role
	CallID  string          `json:"call_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Args    string          `json:"arguments,omitempty"` // function_call arguments (JSON string)
	Output  string          `json:"output,omitempty"`    // function_call_output payload
	ID      string          `json:"id,omitempty"`        // item id for assistant output_text messages
	// Reasoning item replay (sa-54): the summary parts and encrypted
	// chain-of-thought are echoed back verbatim in stateless multi-turn
	// tool loops. For non-reasoning items both stay empty and are omitted.
	Summary          json.RawMessage `json:"summary,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
	// RawReplay (sa-62) carries a verbatim output item (hosted tool call such
	// as web_search_call) that must be echoed back byte-for-byte under
	// store=false; see MarshalJSON.
	RawReplay json.RawMessage `json:"-"`
}

// MarshalJSON emits RawReplay verbatim when set - hosted-tool items replay
// byte-for-byte, so re-encoding through the structured fields is not an
// option. Without RawReplay the structured fields are marshalled as before.
func (i responsesInputItem) MarshalJSON() ([]byte, error) {
	if len(i.RawReplay) > 0 {
		return i.RawReplay, nil
	}
	type plain responsesInputItem
	return json.Marshal(plain(i))
}

type responsesTool struct {
	// Type is "function" for declared client tools, or a hosted tool type
	// ("web_search", "web_search_preview", ...) for server-side tools (sa-62).
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"` // hosted tools carry no name
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

type responsesRequest struct {
	Model           string               `json:"model"`
	Input           []responsesInputItem `json:"input"`
	Tools           []responsesTool      `json:"tools,omitempty"`
	ToolChoice      json.RawMessage      `json:"tool_choice,omitempty"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
	Stream          bool                 `json:"stream,omitempty"`
	Reasoning       *responsesReasoning  `json:"reasoning,omitempty"`
	Store           *bool                `json:"store,omitempty"`
	// Include asks the API to return encrypted reasoning tokens inside
	// reasoning items so they can be replayed statelessly (sa-54).
	Include []string `json:"include,omitempty"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// ---- message mapping ----

func responsesTextPart(text string) map[string]any {
	return map[string]any{"type": "input_text", "text": text}
}

func responsesImagePart(mime, base64 string) map[string]any {
	return map[string]any{"type": "input_image", "image_url": "data:" + mime + ";base64," + base64}
}

// buildResponsesInput converts ggcode messages into Responses `input` items,
// preserving protocol order: an assistant message's text is emitted before the
// function_call items it contains, and tool results become function_call_output
// items in the same position they appear. tool_calls and tool_results are never
// separated by a role boundary violation - both are flat items in one input array.
func buildResponsesInput(messages []Message) []responsesInputItem {
	var items []responsesInputItem
	// sa-67: call ids already seen as apply_patch_call blocks in this history;
	// their tool_result items map back to apply_patch_call_output instead of
	// function_call_output. A single forward pass suffices: a tool_result is
	// always preceded in history by its assistant tool_use block.
	applyPatchCalls := make(map[string]bool)
	for _, msg := range messages {
		var textParts []map[string]any
		isToolRole := msg.Role == "tool"
		// flushText emits accumulated text as a message item so that, within an
		// assistant turn, the text precedes the function_call items that follow
		// it in block order.
		flushText := func() {
			if len(textParts) == 0 {
				return
			}
			switch msg.Role {
			case "system":
				var plain []string
				for _, part := range textParts {
					if t, ok := part["text"].(string); ok {
						plain = append(plain, t)
					}
				}
				items = append(items, responsesInputItem{Role: "system", Content: mustJSON(strings.Join(plain, "\n"))})
			case "assistant":
				outParts := make([]map[string]any, 0, len(textParts))
				for _, part := range textParts {
					outParts = append(outParts, map[string]any{"type": "output_text", "text": part["text"]})
				}
				items = append(items, responsesInputItem{Role: "assistant", Content: mustJSON(outParts)})
			default: // user (and anything else input-facing)
				if msg.Role == "user" && len(textParts) == 1 {
					if t, ok := textParts[0]["text"].(string); ok {
						items = append(items, responsesInputItem{Role: "user", Content: mustJSON(t)})
						textParts = nil
						return
					}
				}
				items = append(items, responsesInputItem{Role: "user", Content: mustJSON(textParts)})
			}
			textParts = nil
		}
		for _, b := range msg.Content {
			switch b.Type {
			case "tool_result":
				out := b.Output
				if out == "" && len(b.Images) == 0 {
					out = "(empty output)"
				}
				if applyPatchCalls[b.ToolID] {
					// sa-67: route the executor's outcome back as the
					// apply_patch_call_output item the Responses API expects;
					// status mirrors the Result error flag (failed/completed).
					items = append(items, responsesInputItem{
						Type:      "apply_patch_call_output",
						CallID:    b.ToolID,
						RawReplay: responsesApplyPatchOutputReplay(b.ToolID, out, b.IsError),
					})
					continue
				}
				items = append(items, responsesInputItem{Type: "function_call_output", CallID: b.ToolID, Output: out})
			case "tool_use":
				flushText()
				if b.ToolName == applyPatchInternalToolName {
					// sa-67: replay the model's apply_patch_call verbatim so the
					// stateless request re-presents its V4A patch (store=false),
					// then record the call id so the paired tool_result maps to an
					// apply_patch_call_output item below.
					items = append(items, responsesInputItem{
						Type:      "apply_patch_call",
						CallID:    b.ToolID,
						RawReplay: responsesApplyPatchCallReplay(b.ToolID, b.Input),
					})
					applyPatchCalls[b.ToolID] = true
					continue
				}
				args := string(b.Input)
				if args == "" || args == "null" {
					args = "{}"
				}
				items = append(items, responsesInputItem{Type: "function_call", CallID: b.ToolID, Name: b.ToolName, Args: args})
			case "server_tool":
				// sa-62: hosted web_search items from a previous Responses turn.
				// Replay the raw item verbatim - store=false discards server
				// state, so an unechoed web_search_call loses the search context.
				// Foreign provider blocks fail the type probe and are dropped,
				// keeping cross-provider failover leak-free.
				if it, ok := decodeResponsesServerToolItem(b.Raw); ok {
					flushText()
					items = append(items, it)
				}
			case "text":
				if isToolRole {
					// Text in a tool-role message is decoration; the output is the payload.
					continue
				}
				if b.Text == "" {
					// Reasoning-only block: streamed reasoning summaries land in
					// ReasoningContent, and the encrypted reasoning item block
					// replays them; an empty output_text part would be noise.
					continue
				}
				textParts = append(textParts, responsesTextPart(b.Text))
			case "thinking":
				// sa-54: an encrypted Responses reasoning item captured verbatim
				// in a "thinking" block (Anthropic-style carrier). Replay it
				// unchanged: with store=false the API cannot reconstruct the
				// model's reasoning context otherwise. Signatures from other
				// providers (Anthropic/Gemini) fail the decode and are skipped,
				// so cross-provider failover never leaks foreign blocks.
				if it, ok := decodeResponsesReasoningItem(b.ThinkingSignature); ok {
					flushText()
					items = append(items, it)
				}
			case "image":
				if isToolRole {
					continue
				}
				textParts = append(textParts, responsesImagePart(b.ImageMIME, b.ImageData))
			}
		}
		flushText()
	}
	return items
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// responsesIncludeEncryptedReasoning asks the API to embed encrypted
// reasoning tokens in reasoning item outputs (required for stateless
// multi-turn replay when store=false).
const responsesIncludeEncryptedReasoning = "reasoning.encrypted_content"

// decodeResponsesReasoningItem parses a raw reasoning item captured from a
// previous response (stored in a "thinking" block's ThinkingSignature) and
// reports whether it is replayable. Only items that actually carry encrypted
// reasoning content are replayed: bare item ids cannot be resolved once
// store=false has discarded server state, and anything that is not a
// Responses reasoning item (Anthropic/Gemini signatures from a failed-over
// session) fails the type probe and is dropped.
func decodeResponsesReasoningItem(raw string) (responsesInputItem, bool) {
	if raw == "" {
		return responsesInputItem{}, false
	}
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(raw), &probe) != nil || probe.Type != "reasoning" {
		return responsesInputItem{}, false
	}
	var item responsesInputItem
	if json.Unmarshal([]byte(raw), &item) != nil || item.EncryptedContent == "" {
		return responsesInputItem{}, false
	}
	return item, true
}

func (p *OpenAIResponsesProvider) buildRequest(messages []Message, tools []ToolDefinition, stream bool) (*responsesRequest, error) {
	req := &responsesRequest{
		Model:   p.model,
		Input:   buildResponsesInput(messages),
		Stream:  stream,
		Store:   boolPtr(false), // stateless replay; no server-side conversation state
		Include: []string{responsesIncludeEncryptedReasoning},
	}
	budget := p.maxTokensOverride
	if budget <= 0 {
		budget = p.maxTokens
	}
	if budget > 0 {
		req.MaxOutputTokens = budget
	}
	if p.reasoningEffort != "" {
		req.Reasoning = &responsesReasoning{Effort: p.reasoningEffort}
	}
	for _, t := range tools {
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		req.Tools = append(req.Tools, responsesTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: params})
	}
	// Hosted (server-side) tools (sa-62): appended after the declared
	// function tools. OpenAI addresses them purely by type.
	for _, st := range p.serverTools {
		if rt, ok := responsesHostedTool(st); ok {
			req.Tools = append(req.Tools, rt)
		}
	}
	switch strings.ToLower(p.toolChoice) {
	case "required":
		req.ToolChoice = json.RawMessage(`"required"`)
	case "none":
		req.ToolChoice = json.RawMessage(`"none"`)
	case "", "auto":
		// API default
	default:
		// Pass through structured choices ({"type":"function", ...}) verbatim.
		req.ToolChoice = json.RawMessage(p.toolChoice)
	}
	return req, nil
}

func boolPtr(b bool) *bool { return &b }

// ---- HTTP plumbing ----

func (p *OpenAIResponsesProvider) post(ctx context.Context, req *responsesRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("responses: encoding request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("responses API error %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return resp, nil
}

// ---- non-streaming Chat ----

func (p *OpenAIResponsesProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResponse, error) {
	req, err := p.buildRequest(messages, tools, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Output []json.RawMessage `json:"output"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("responses: decoding response: %w", err)
	}

	out := &ChatResponse{Message: Message{Role: "assistant"}}
	var texts []string
	for _, rawItem := range payload.Output {
		var item struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			ID      string `json:"id"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			continue
		}
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" && c.Text != "" {
					texts = append(texts, c.Text)
				}
			}
		case "function_call":
			args := item.Arguments
			if args == "" {
				args = "{}"
			}
			if !json.Valid([]byte(args)) {
				if repaired, ok := RepairJSON([]byte(args)); ok {
					args = string(repaired)
				}
			}
			out.Message.Content = append(out.Message.Content,
				ToolUseBlock(item.CallID, item.Name, json.RawMessage(args)))
		case "reasoning":
			// sa-54: keep the raw reasoning item verbatim (thinking-block
			// carrier) so the next stateless request replays the model's
			// encrypted chain-of-thought context.
			out.Message.Content = append(out.Message.Content, ContentBlock{
				Type:              "thinking",
				ThinkingSignature: string(rawItem),
			})
		case "web_search_call":
			// sa-62: hosted web_search executed inside the Responses API;
			// keep the raw item verbatim so the next stateless request
			// echoes the full search exchange back.
			out.Message.Content = append(out.Message.Content, responsesServerToolBlock(rawItem))
		default:
			// sa-67: apply_patch_call is a client-executed tool call. Convert it
			// to a ToolUseBlock naming the hidden apply_patch executor so the
			// standard agent loop applies the V4A diff and returns a tool_result,
			// which buildResponsesInput maps back to apply_patch_call_output.
			if id, op, ok := decodeResponsesApplyPatchCall(rawItem); ok {
				args := op
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				out.Message.Content = append(out.Message.Content,
					ToolUseBlock(id, applyPatchInternalToolName, args))
			}
		}
	}
	if len(texts) > 0 {
		out.Message.Content = append(out.Message.Content, TextBlock(strings.Join(texts, "")))
	}
	out.Usage = TokenUsage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens}
	if payload.Status == "incomplete" {
		out.StopReason = "max_tokens"
	}
	return out, nil
}

// ---- streaming ----

func (p *OpenAIResponsesProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition) (<-chan StreamEvent, error) {
	req, err := p.buildRequest(messages, tools, true)
	if err != nil {
		return nil, err
	}
	httpResp, err := p.post(ctx, req)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent, 64)
	go func() {
		defer close(ch)
		defer httpResp.Body.Close()

		scanner := bufio.NewScanner(httpResp.Body)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

		var doneSent bool
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" || data == "[DONE]" {
				continue
			}
			var ev struct {
				Type  string          `json:"type"`
				Delta string          `json:"delta"`
				Item  json.RawMessage `json:"item"`
				Resp  json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			switch ev.Type {
			case "response.output_text.delta":
				if ev.Delta != "" {
					ch <- StreamEvent{Type: StreamEventText, Text: ev.Delta}
				}
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				if ev.Delta != "" {
					ch <- StreamEvent{Type: StreamEventReasoning, Text: ev.Delta}
				}
			case "response.output_item.added":
				var item struct {
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}
				if json.Unmarshal(ev.Item, &item) == nil && item.Type == "function_call" && item.CallID != "" {
					args := item.Arguments
					if args == "" {
						args = "{}"
					}
					ch <- StreamEvent{Type: StreamEventToolCallChunk, Tool: ToolCallDelta{
						ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(args),
					}}
				}
				// sa-67: apply_patch_call is a client-executed tool call; surface
				// it through the same channel as function_call so the agent loop
				// runs the V4A executor and pairs the result back.
				if id, op, ok := decodeResponsesApplyPatchCall(ev.Item); ok {
					args := op
					if len(args) == 0 {
						args = json.RawMessage("{}")
					}
					ch <- StreamEvent{Type: StreamEventToolCallChunk, Tool: ToolCallDelta{
						ID: id, Name: applyPatchInternalToolName, Arguments: args,
					}}
				}
			case "response.output_item.done":
				var item struct {
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}
				if json.Unmarshal(ev.Item, &item) == nil && item.Type == "reasoning" {
					// sa-54: forward the raw item through the reasoning channel;
					// the agent's thinking accumulator stores it verbatim in a
					// "thinking" block for stateless replay. Text stays empty so
					// UIs render nothing for the opaque payload.
					ch <- StreamEvent{Type: StreamEventReasoning, ThinkingSignature: string(ev.Item)}
				}
				if json.Unmarshal(ev.Item, &item) == nil && item.Type == "function_call" && item.CallID != "" {
					args := item.Arguments
					if args == "" {
						args = "{}"
					}
					if !json.Valid([]byte(args)) {
						if repaired, ok := RepairJSON([]byte(args)); ok {
							args = string(repaired)
						}
					}
					ch <- StreamEvent{Type: StreamEventToolCallDone, Tool: ToolCallDelta{
						ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(args),
					}}
				}
				// sa-67: final apply_patch_call item; emit the authoritative done
				// event so the call is executed exactly once per patch.
				if id, op, ok := decodeResponsesApplyPatchCall(ev.Item); ok {
					args := op
					if len(args) == 0 {
						args = json.RawMessage("{}")
					}
					ch <- StreamEvent{Type: StreamEventToolCallDone, Tool: ToolCallDelta{
						ID: id, Name: applyPatchInternalToolName, Arguments: args,
					}}
				}
				if json.Unmarshal(ev.Item, &item) == nil && item.Type == "web_search_call" {
					// sa-62: hosted web_search item finished; forward it once
					// through the server-tool channel so the agent stores the
					// raw item and the next stateless request echoes it back.
					ch <- StreamEvent{Type: StreamEventServerTool, Block: responsesServerToolBlock(ev.Item)}
				}
			case "response.completed", "response.incomplete":
				usage, stop := parseResponsesFinal(data)
				ch <- StreamEvent{Type: StreamEventDone, Usage: usage, Truncated: stop == "max_tokens" || ev.Type == "response.incomplete"}
				doneSent = true
			case "response.failed":
				var respErr struct {
					Response struct {
						Error struct {
							Message string `json:"message"`
						} `json:"error"`
					} `json:"response"`
				}
				_ = json.Unmarshal([]byte(data), &respErr)
				msg := respErr.Response.Error.Message
				if msg == "" {
					msg = "responses stream failed"
				}
				ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("responses: %s", msg)}
				doneSent = true
			case "error":
				var errPayload struct {
					Message string `json:"message"`
				}
				_ = json.Unmarshal([]byte(data), &errPayload)
				msg := errPayload.Message
				if msg == "" {
					msg = "responses stream error"
				}
				ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("responses: %s", msg)}
				doneSent = true
			}
			if doneSent {
				return
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			ch <- StreamEvent{Type: StreamEventError, Error: fmt.Errorf("responses stream: %w", err)}
			return
		}
		if !doneSent {
			if ctx.Err() != nil {
				ch <- StreamEvent{Type: StreamEventError, Error: ctx.Err()}
				return
			}
			// Stream closed without a terminal event: still emit Done so the
			// agent loop finalizes the turn instead of hanging.
			ch <- StreamEvent{Type: StreamEventDone}
		}
	}()
	return ch, nil
}

// parseResponsesFinal extracts usage and incomplete_details from a
// response.completed / response.incomplete payload.
func parseResponsesFinal(data string) (*TokenUsage, string) {
	var final struct {
		Response struct {
			Usage struct {
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				InputTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputTokensDetails struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
			IncompleteDetails struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(data), &final); err != nil {
		return nil, ""
	}
	u := &TokenUsage{
		InputTokens:  final.Response.Usage.InputTokens,
		OutputTokens: final.Response.Usage.OutputTokens,
		CacheRead:    final.Response.Usage.InputTokensDetails.CachedTokens,
	}
	return u, final.Response.IncompleteDetails.Reason
}
