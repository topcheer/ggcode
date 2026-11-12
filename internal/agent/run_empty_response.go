package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// #5543: loop-core extraction. The empty-response guard of the stream
// loop (fully-blocked policy responses and consecutive-empty-response
// abort) becomes an explicit method; behavior unchanged (verbatim body).

// emptyRespAction tells the stream loop what to do after the guard.
type emptyRespAction int

const (
	emptyRespProceed  emptyRespAction = iota // response has content (or guard passed); streak reset
	emptyRespRetry                           // retry this iteration (empty response nudged)
	emptyRespTerminal                        // stop the run; paired error is final
)

// checkEmptyResponse classifies an empty chat response (verbatim order):
// policy-blocked responses abort immediately; otherwise the consecutive
// streak is bumped and a retry prompt injected, aborting after 3 in a
// row. Non-empty responses reset the streak to 0.
func (a *Agent) checkEmptyResponse(
	i int,
	resp *provider.ChatResponse,
	textBuf string,
	toolCalls []provider.ToolCallDelta,
	policyBlocked bool,
	onEvent func(provider.StreamEvent),
	consecutiveEmptyResponses int,
) (emptyRespAction, int, error) {
	if resp.Usage.OutputTokens == 0 && len(toolCalls) == 0 &&
		strings.TrimSpace(textBuf) == "" && !hasNonThinkingContent(resp.Message.Content) {

		if policyBlocked {
			debug.Log("agent", "Iteration %d: response fully blocked by provider policy, not retrying", i+1)
			onEvent(provider.StreamEvent{
				Type: provider.StreamEventSystem,
				Text: "[Response blocked by provider safety policy - not retrying. Try rephrasing the request.] ",
			})
			if len(resp.Message.Content) > 0 {
				a.contextManager.Add(resp.Message)
			}

			return emptyRespTerminal, consecutiveEmptyResponses, fmt.Errorf("agent: response blocked by provider safety filter (task did not complete)")
		}
		consecutiveEmptyResponses++
		debug.Log("agent", "Iteration %d: empty response detected (consecutive=%d, input_tokens=%d)",
			i+1, consecutiveEmptyResponses, resp.Usage.InputTokens)
		if consecutiveEmptyResponses >= 3 {
			debug.Log("agent", "too many consecutive empty responses (%d), aborting", consecutiveEmptyResponses)

			onEvent(provider.StreamEvent{
				Type: provider.StreamEventText,
				Text: "[run aborted - the model returned 3 consecutive empty responses; the task did not complete]\n",
			})
			return emptyRespTerminal, consecutiveEmptyResponses, fmt.Errorf("agent: aborted after %d consecutive empty responses", consecutiveEmptyResponses)
		}

		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: "The previous response was empty. Please try again.",
			}},
		})
		return emptyRespRetry, consecutiveEmptyResponses, nil
	}
	consecutiveEmptyResponses = 0
	return emptyRespProceed, consecutiveEmptyResponses, nil
}
