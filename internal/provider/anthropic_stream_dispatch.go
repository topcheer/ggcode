package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// sleepBeforeRetry emits the "[Retry n/m...]" system event and sleeps under
// the shared retry budget (#722). Mirrors openai.go's sleepBeforeRetry except
// the send is a bare blocking ch<- (historical anthropic semantics — this
// provider has no ctx-aware sendEvent). Extracted verbatim from ChatStream's
// stream-phase retry block. Returns (slept, terminalErr): terminalErr non-nil
// means the budget is exhausted — caller must emit Error{terminalErr}, set
// streamError, and return.
func (p *AnthropicProvider) sleepBeforeRetry(ctx context.Context, ch chan<- StreamEvent, budget *retryBudget, cause error, attempt int) (bool, error) {
	delay := retryDelay(cause, attempt)
	ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, p.policy.attempts(), delay)}
	if sleepErr := budget.sleep(ctx, delay); sleepErr != nil {
		// #722: budget exhausted — stop retrying now; wrap with the
		// sentinel so the failover layer switches immediately.
		if sleepErr == errRetryBudgetExhausted {
			sleepErr = fmt.Errorf("%w: %w", errRetryBudgetExhausted, cause)
		}
		return false, sleepErr
	}
	return true, nil
}

// handleContentBlockStart registers or directly emits content_block_start
// blocks (tool_use / server_tool_use / tool results / thinking variants).
// Verbatim extraction from ChatStream — no branch/order/guard changes.
// Returns whether the block produced a user-visible event.
func handleContentBlockStart(ch chan<- StreamEvent, toolCalls map[int]*ToolCallDelta, event anthropic.MessageStreamEventUnion) bool {
	cb := event.ContentBlock
	switch cb.Type {
	case "tool_use":
		idx := int(event.Index)
		tc := &ToolCallDelta{Index: idx, ID: cb.ID, Name: cb.Name}
		if tu := cb.AsToolUse(); tu.JSON.Caller.Valid() {
			tc.Caller = callerRawOf(tu.Caller) // PTC echo-back
		}
		toolCalls[idx] = tc
		debug.Log("anthropic", "content_block_start tool_use id=%s name=%s idx=%d", cb.ID, cb.Name, idx)
	case "server_tool_use":
		// Anthropic server-side tool invocation (executed in-API).
		// Input arrives via input_json_delta like a client tool_use,
		// but the block must NOT be surfaced as a client tool call —
		// it is emitted verbatim at content_block_stop.
		idx := int(event.Index)
		toolCalls[idx] = &ToolCallDelta{Index: idx, ID: cb.ID, Name: cb.Name, ServerTool: true}
	case "web_search_tool_result", "web_fetch_tool_result", "tool_search_tool_result":
		// Result blocks arrive complete (no deltas). Keep the raw
		// JSON verbatim for echo-back on the next request. For the
		// Tool Search Tool this preserves the tool_reference
		// expansions so the API does not treat them as deferred.
		ch <- StreamEvent{
			Type:  StreamEventServerTool,
			Block: ContentBlock{Type: cb.Type, Raw: json.RawMessage(cb.RawJSON())},
		}
		return true
	case "code_execution_tool_result":
		// PTC: code execution result, executed in-API inside the
		// container. Arrives complete; echo verbatim like the web
		// server-tool results above.
		ch <- StreamEvent{
			Type:  StreamEventServerTool,
			Block: ContentBlock{Type: cb.Type, Raw: json.RawMessage(cb.RawJSON())},
		}
		return true
	case "thinking":
		debug.Log("anthropic", "content_block_start thinking idx=%d sig_len=%d", event.Index, len(cb.Signature))
		toolCalls[int(event.Index)] = &ToolCallDelta{
			Index: int(event.Index),
			ID:    cb.Signature, // carries signature for echo-back
		}
		// Emit reasoning event with signature so agent can store it
		ch <- StreamEvent{Type: StreamEventReasoning, ThinkingSignature: cb.Signature}
		return true
	case "redacted_thinking":
		debug.Log("anthropic", "content_block_start redacted_thinking idx=%d data_len=%d", event.Index, len(cb.Data))
		// Register with empty Name (like the thinking branch)
		// so content_block_stop's `tc.Name != ""` check skips
		// it — redacted thinking is reasoning data, not a
		// tool call. Echo-back happens via the reasoning
		// event below (#224).
		toolCalls[int(event.Index)] = &ToolCallDelta{
			Index: int(event.Index),
			ID:    cb.Data, // carries redacted data for echo-back
		}
		// Emit reasoning event with redacted data for echo-back
		ch <- StreamEvent{Type: StreamEventReasoning, Text: "__redacted_thinking__", ThinkingSignature: cb.Data}
		return true
	}
	return false
}

// handleContentBlockStop finalizes a content block: server_tool_use echo-back
// (incl. PTC code_execution tool_use re-shaping) or client tool_use
// ToolCallDone. Returns (charsDelta, emittedDelta); nothing happened for
// non-tool blocks. Verbatim extraction from ChatStream.
func handleContentBlockStop(ch chan<- StreamEvent, toolCalls map[int]*ToolCallDelta, idx int) (int, bool) {
	if tc, ok := toolCalls[idx]; ok && tc.ServerTool {
		debug.Log("anthropic", "content_block_stop server_tool_use id=%s name=%s", tc.ID, tc.Name)
		if tc.Name == "code_execution" {
			// PTC: the code execution call streams as a regular
			// tool_use block executed in-API. Store the FULL tool_use
			// block fields so the next request echoes back a valid
			// tool_use (type+caller), not a bare server_tool_use.
			ch <- StreamEvent{
				Type: StreamEventServerTool,
				Block: ContentBlock{
					Type:      "tool_use",
					ToolID:    tc.ID,
					ToolName:  tc.Name,
					Input:     tc.Arguments,
					CallerRaw: tc.Caller,
				},
			}
		} else {
			ch <- StreamEvent{
				Type: StreamEventServerTool,
				Block: ContentBlock{
					Type: "server_tool_use",
					ID:   tc.ID,
					Raw:  serverToolUseRaw(tc.ID, tc.Name, tc.Arguments),
				},
			}
		}
		delete(toolCalls, idx)
		return 0, true
	} else if tc, ok := toolCalls[idx]; ok && tc.Name != "" {
		debug.Log("anthropic", "content_block_stop tool_call id=%s name=%s args=%s", tc.ID, tc.Name, string(tc.Arguments))
		ch <- StreamEvent{
			Type: StreamEventToolCallDone,
			Tool: *tc,
		}
		delete(toolCalls, idx)
		return len(tc.Name) + len(tc.Arguments), true
	}
	return 0, false
}
