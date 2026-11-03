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
// the send is a bare blocking ch<- (historical anthropic semantics - this
// provider has no ctx-aware sendEvent). Extracted verbatim from ChatStream's
// stream-phase retry block. Returns (slept, terminalErr): terminalErr non-nil
// means the budget is exhausted - caller must emit Error{terminalErr}, set
// streamError, and return.
func (p *AnthropicProvider) sleepBeforeRetry(ctx context.Context, ch chan<- StreamEvent, budget *retryBudget, cause error, attempt int) (bool, error) {

	if isTimeoutClassError(cause) {
		budget.unlimited = true
	}
	delay := retryDelay(cause, attempt)
	ch <- StreamEvent{Type: StreamEventSystem, Text: fmt.Sprintf("[Retry %d/%d, waiting %v...] ", attempt+1, p.policy.attempts(), delay)}
	if notice := TimeoutRetryNotice(cause, attempt); notice != "" {
		ch <- StreamEvent{Type: StreamEventSystem, Text: notice}
	}
	if sleepErr := budget.sleep(ctx, delay); sleepErr != nil {

		if sleepErr == errRetryBudgetExhausted {
			sleepErr = fmt.Errorf("%w: %w", errRetryBudgetExhausted, cause)
		}
		return false, sleepErr
	}
	return true, nil
}

// handleContentBlockStart registers or directly emits content_block_start
// blocks (tool_use / server_tool_use / tool results / thinking variants).
// Verbatim extraction from ChatStream - no branch/order/guard changes.
// Returns whether the block produced a user-visible event.
func handleContentBlockStart(ch chan<- StreamEvent, toolCalls map[int]*ToolCallDelta, event anthropic.MessageStreamEventUnion) bool {
	cb := event.ContentBlock
	switch cb.Type {
	case "tool_use":
		idx := int(event.Index)
		tc := &ToolCallDelta{Index: idx, ID: cb.ID, Name: cb.Name}
		if tu := cb.AsToolUse(); tu.JSON.Caller.Valid() {
			tc.Caller = callerRawOf(tu.Caller)
		}
		toolCalls[idx] = tc
		debug.Log("anthropic", "content_block_start tool_use id=%s name=%s idx=%d", cb.ID, cb.Name, idx)
	case "server_tool_use":

		idx := int(event.Index)
		toolCalls[idx] = &ToolCallDelta{Index: idx, ID: cb.ID, Name: cb.Name, ServerTool: true}
	case "web_search_tool_result", "web_fetch_tool_result", "tool_search_tool_result":

		ch <- StreamEvent{
			Type:  StreamEventServerTool,
			Block: ContentBlock{Type: cb.Type, Raw: json.RawMessage(cb.RawJSON())},
		}
		return true
	case "code_execution_tool_result":

		ch <- StreamEvent{
			Type:  StreamEventServerTool,
			Block: ContentBlock{Type: cb.Type, Raw: json.RawMessage(cb.RawJSON())},
		}
		return true
	case "thinking":
		debug.Log("anthropic", "content_block_start thinking idx=%d sig_len=%d", event.Index, len(cb.Signature))
		toolCalls[int(event.Index)] = &ToolCallDelta{
			Index: int(event.Index),
			ID:    cb.Signature,
		}

		ch <- StreamEvent{Type: StreamEventReasoning, ThinkingSignature: cb.Signature}
		return true
	case "redacted_thinking":
		debug.Log("anthropic", "content_block_start redacted_thinking idx=%d data_len=%d", event.Index, len(cb.Data))

		toolCalls[int(event.Index)] = &ToolCallDelta{
			Index: int(event.Index),
			ID:    cb.Data,
		}

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
