package agent

import (
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// streamStallThreshold is the maximum gap between stream events before a
// stall warning is emitted. 30s is generous enough for extended reasoning
// pauses (providers like Anthropic can have 10-20s gaps between thinking
// chunks) but catches real network degradation or server-side hangs.
const streamStallThreshold = 30 * time.Second

// streamWithStallDetection wraps a provider stream channel, injecting advisory
// StreamEventSystem events when no data arrives for stallThreshold duration.
//
// This addresses a real Streaming UX gap: when a provider stalls (network
// degradation, server overload, or dropped connection), the user sees a frozen
// spinner with no feedback. The stall warning flows through the normal
// StreamEventSystem channel so existing UI handlers display it without
// additional wiring -- the StreamEventSystem case in streamChatResponse
// already forwards these to onEvent.
//
// Design:
//   - Advisory only: does not cancel or interrupt the stream
//   - Single warning per stall period: resets when a real event arrives
//   - Zero LLM cost: purely deterministic timer-based detection
//   - Thread-safe: all events (including stall warnings) flow through one
//     channel, avoiding concurrent onEvent calls
func streamWithStallDetection(stream <-chan provider.StreamEvent, stallThreshold time.Duration) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent)
	go func() {
		defer close(out)
		timer := time.NewTimer(stallThreshold)
		defer timer.Stop()
		warned := false
		for {
			select {
			case event, ok := <-stream:
				if !ok {
					return
				}
				warned = false
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(stallThreshold)
				out <- event
			case <-timer.C:
				if !warned {
					warned = true
					out <- provider.StreamEvent{
						Type: provider.StreamEventSystem,
						Text: fmt.Sprintf("stream stall: no data for %s, connection may be degraded", stallThreshold),
					}
				}
				timer.Reset(stallThreshold)
			}
		}
	}()
	return out
}
