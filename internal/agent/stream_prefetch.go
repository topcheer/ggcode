package agent

import (
	"context"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

// Stream-Overlapped Tool Execution ("Act While Thinking").
//
// PASTE (arXiv:2603.18897, 2026) and Act While Thinking (Microsoft Research,
// 2026) show that agent latency is dominated by the strictly serial
// generate→execute loop: harnesses leave tool latency exposed on the critical
// path because execution only starts after the LLM finishes its entire
// response. ggcode already parallelizes within a batch (parallel_tools.go),
// overlaps concurrent waits (wait_parallel.go) and speculates across turns
// (speculate.go), but a read-only call whose arguments JSON completes early
// in the stream still waits for ALL remaining generation (trailing text and
// subsequent tool calls) before executing.
//
// This file closes that gap: when a tool call's JSON closes
// (StreamEventToolCallDone) and the call is on the read-only safe list, its
// execution is dispatched immediately while the LLM keeps generating. The
// resulting future is harvested after the stream ends and merged into the
// same preExecuted map the sequential loop already consumes, so:
//   - Permission semantics are unchanged (usePreExecutedWithPermission still
//     gates consumption; a deny discards the pre-computed read-only result).
//   - The batch pre-executor skips these indices (no double execution).
//   - Cancellation (Esc mid-stream) propagates via ctx; an abandoned
//     read-only call finishes harmlessly and its result is dropped.
//
// Losslessness ("Speculative Actions", arXiv:2510.04371: commit only when
// predictions match): unlike cross-turn speculation, the exact invocation is
// guaranteed to be consumed this turn - ToolCallDone carries final arguments
// - with one exception re-checked at harvest: a read whose scan scope
// collides with a same-batch mutation (unknown while streaming) is discarded
// so the sequential loop re-executes it post-mutation with emitted-order
// semantics (#1475-A race guard, reused verbatim).

// streamPrefetchMaxConcurrent bounds in-flight stream-overlapped executions.
// Matches parallelMaxConcurrent (W&D optimal width).
const streamPrefetchMaxConcurrent = parallelMaxConcurrent

// streamPrefetcher tracks read-only tool calls dispatched during streaming.
// One instance per streamChatResponse call (reset at stream start).
type streamPrefetcher struct {
	mu       sync.Mutex
	futures  map[int]*streamPrefetchFuture // keyed by toolCalls slice index
	inflight int
	started  int
}

// streamPrefetchFuture is one overlapped execution in progress.
type streamPrefetchFuture struct {
	done   chan struct{}
	ok     bool
	result tool.Result
	dur    time.Duration
}

func newStreamPrefetcher() *streamPrefetcher {
	return &streamPrefetcher{futures: make(map[int]*streamPrefetchFuture)}
}

// mayStart dispatches a read-only tool call for overlapped execution while
// the LLM keeps generating. idx is the index the call will occupy in the
// collected toolCalls slice. Returns true if dispatched.
func (sp *streamPrefetcher) mayStart(ctx context.Context, a *Agent, tc provider.ToolCallDelta, idx int) bool {
	if sp == nil || ctx.Err() != nil || tc.ServerTool || tc.Name == "" {
		return false
	}
	if !speculativeSafeTools[tc.Name] {
		return false
	}
	sp.mu.Lock()
	if sp.inflight >= streamPrefetchMaxConcurrent || sp.futures[idx] != nil {
		sp.mu.Unlock()
		return false
	}
	f := &streamPrefetchFuture{done: make(chan struct{})}
	sp.futures[idx] = f
	sp.inflight++
	sp.started++
	sp.mu.Unlock()

	go func() {
		// Recover registers first so it runs LAST (LIFO): done is closed
		// even if the tool panics - harvest must never block forever.
		defer safego.Recover("agent.streamPrefetch")
		defer func() {
			sp.mu.Lock()
			sp.inflight--
			sp.mu.Unlock()
			close(f.done)
		}()
		result, dur, ok := a.preExecOne(ctx, pending{index: idx, name: tc.Name, args: tc.Arguments})
		if ok {
			f.result, f.dur, f.ok = result, dur, true
		}
	}()
	debug.Log("parallel", "stream-overlapped dispatch %s (index=%d)", tc.Name, idx)
	return true
}

// has reports whether the call at idx was dispatched during the stream.
func (sp *streamPrefetcher) has(idx int) bool {
	if sp == nil {
		return false
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.futures[idx] != nil
}

// harvestStreamPrefetch waits for the overlapped executions after the stream
// ends and returns the committed results keyed by tool-call index. The
// commit decision applies the batch mutation-collision guard with the now
// complete batch; discarded indices are absent so the sequential loop
// re-executes them through the normal gated path.
func (a *Agent) harvestStreamPrefetch(ctx context.Context, toolCalls []provider.ToolCallDelta) map[int]preExecutedResult {
	sp := a.streamPrefetch
	if sp == nil || len(sp.futures) == 0 {
		return nil
	}
	mutatedPaths, schedulable := partitionBatchForParallelism(toolCalls)
	if !schedulable {
		// Batch contains a mutation with an unknowable write set: discard
		// every overlapped result (in-flight goroutines self-complete; their
		// read-only results are simply dropped).
		debug.Log("parallel", "stream-overlap: batch not schedulable, discarding %d overlapped calls", len(sp.futures))
		return nil
	}
	results := make(map[int]preExecutedResult, len(sp.futures))
	for idx, f := range sp.snapshot() {
		if len(mutatedPaths) > 0 && idx < len(toolCalls) &&
			readAffectedByMutation(toolCalls[idx].Name, toolCalls[idx].Arguments, mutatedPaths) {
			debug.Log("parallel", "stream-overlap: discarding %s (index=%d), collides with batch mutation",
				toolCalls[idx].Name, idx)
			continue
		}
		select {
		case <-f.done:
			if f.ok {
				results[idx] = preExecutedResult{f.result, f.dur}
			}
		case <-ctx.Done():
			return results
		}
	}
	if len(results) == 0 {
		return nil
	}
	debug.Log("parallel", "stream-overlap: committed %d/%d overlapped results", len(results), sp.started)
	return results
}

// snapshot copies the futures map under lock.
func (sp *streamPrefetcher) snapshot() map[int]*streamPrefetchFuture {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	out := make(map[int]*streamPrefetchFuture, len(sp.futures))
	for k, v := range sp.futures {
		out[k] = v
	}
	return out
}
