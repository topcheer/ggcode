package agent

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
)

// Intra-decode Speculative Tool Execution — the client-side strategy from
// "Optimizing Agentic Language Model Inference via Speculative Tool Calls"
// (arXiv:2512.15834) and "Speculative Actions" (arXiv:2510.04371).
//
// Prior latency work in this package attacks two windows:
//   - speculate.go (PASTE): pattern-based pre-execution in the gap BETWEEN
//     LLM turns.
//   - parallel_tools.go (LLMCompiler/W&D): concurrent execution of read-only
//     calls AFTER the full response has been decoded.
//
// Both leave a third window open: WITHIN a streaming response, a tool call's
// arguments are fully known the moment its ToolCallDone event arrives, but
// its execution still waits for the model to finish decoding the rest of the
// response (more tool calls, prose, reasoning). On multi-call turns with
// long text tails that is seconds of idle I/O latency.
//
// streamSpeculator closes that window: on each ToolCallDone for a read-only
// tool it starts the execution immediately, overlapping tool latency with
// the remaining decode. Results are consumed in-order by the sequential loop
// through the same preExecuted map the batch path uses, so permission
// gating (#1496), conflict-aware scheduling (parallel_scheduling.go) and
// post-execution detectors are all preserved.
//
// Safety:
//   - Only tools in speculativeSafeTools (read-only, idempotent) speculate.
//   - A later mutating call in the SAME response invalidates pending
//     speculations whose scan scope covers the mutation (file-scoped:
//     precise invalidation via readAffectedByMutation; tree-wide/unknown
//     write sets like run_command: invalidate all — #1475-A/#1590-A lineage).
//   - Context-fill throttling mirrors preExecuteReadOnlyTools.
//   - Max specMaxConcurrent in-flight executions.
//   - A dropped/failed speculation is invisible: the sequential loop simply
//     re-executes the call.

const specStreamMaxWait = specMaxConcurrent // in-flight cap; matches batch width

type specFuture struct {
	idx  int // arrival order == index in the response's toolCalls slice
	name string
	args json.RawMessage
	ch   chan specOutcome // buffered size 1
}

// specOutcome carries the executed result plus an explicit ok flag so a
// failed speculation (zero result) is distinguishable from a legitimate
// empty read result.
type specOutcome struct {
	res preExecutedResult
	ok  bool
}

// streamSpeculator speculatively executes read-only tool calls DURING
// response streaming. One instance per agent-loop iteration (per LLM turn).
type streamSpeculator struct {
	a      *Agent
	ctx    context.Context // derived from the run ctx; cancelled by abort()
	cancel context.CancelFunc

	mu      sync.Mutex
	futures map[string]*specFuture // keyed by tool call ID
	active  int
	n       int // arrival counter
	done    bool
}

func newStreamSpeculator(a *Agent, parent context.Context) *streamSpeculator {
	ctx, cancel := context.WithCancel(parent)
	return &streamSpeculator{
		a:       a,
		ctx:     ctx,
		cancel:  cancel,
		futures: make(map[string]*specFuture),
	}
}

// abort cancels in-flight speculative executions (used when the stream
// errors out and the turn will be retried).
func (s *streamSpeculator) abort() {
	s.cancel()
}

// hasSpeculationDuplicate reports whether the cross-turn speculator cache or
// the memo cache already covers this exact call — executing it again would
// be redundant (mirrors the buildPreExecBatch skip).
func (a *Agent) hasSpeculationDuplicate(name string, args json.RawMessage) bool {
	if a.speculator != nil && a.speculator.hasCached(name, args) {
		return true
	}
	if a.toolMemo != nil {
		if _, hit := a.toolMemo.get(name, args); hit {
			return true
		}
	}
	return false
}

// budgetOK applies the same context-fill throttling as the batch path.
func (s *streamSpeculator) budgetOK() bool {
	if s.a.contextManager == nil {
		return true
	}
	threshold := s.a.contextManager.AutoCompactThreshold()
	if threshold <= 0 {
		return true
	}
	fillRatio := float64(s.a.contextManager.TokenCount()) / float64(threshold)
	if fillRatio >= contextFillCritical {
		return false
	}
	// High fill still allows one in-flight speculation (batch reduces to 1).
	return true
}

// onToolCallDone is invoked for each completed tool call while the response
// is still streaming. Safe for concurrent use with itself (providers emit
// events serially, but do not rely on it).
func (s *streamSpeculator) onToolCallDone(tc provider.ToolCallDelta) {
	if s == nil || tc.Name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	idx := s.n
	s.n++

	if !speculativeSafeTools[tc.Name] {
		// Mutating (or unknown) call: invalidate conflicting speculations.
		if path, scoped := fileScopedMutationPath(tc.Name, tc.Arguments); scoped {
			s.invalidateAffected([]string{path})
		} else {
			// Write set not statically knowable (run_command, delegate, ...):
			// conservatively drop every pending speculation.
			s.invalidateAllLocked()
		}
		return
	}

	if s.active >= specStreamMaxWait || !s.budgetOK() {
		debug.Log("parallel", "stream-spec: skipping %s (idx=%d) — throttled", tc.Name, idx)
		return
	}
	if s.a.hasSpeculationDuplicate(tc.Name, tc.Arguments) {
		return // speculator cache / memo already covers this call
	}

	fut := &specFuture{
		idx:  idx,
		name: tc.Name,
		args: tc.Arguments,
		ch:   make(chan specOutcome, 1),
	}
	s.futures[tc.ID] = fut
	s.active++
	go func() {
		defer safego.Recover("agent.streamSpec")
		out := specOutcome{}
		if r, dur, ok := s.a.preExecOne(s.ctx, pending{name: tc.Name, args: tc.Arguments}); ok {
			out = specOutcome{res: preExecutedResult{result: r, duration: dur}, ok: true}
		}
		fut.ch <- out // buffered; never blocks even if invalidated
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
		debug.Log("parallel", "stream-spec: executed %s in %v (idx=%d, ok=%v)", tc.Name, out.res.duration, idx, out.ok)
	}()
}

// invalidateAffected drops pending speculations whose read scope covers the
// mutated path (the read would race the mutation and hand back stale
// content — the #1475-A failure mode).
func (s *streamSpeculator) invalidateAffected(mutatedPaths []string) {
	for id, fut := range s.futures {
		if readAffectedByMutation(fut.name, fut.args, mutatedPaths) {
			debug.Log("parallel", "stream-spec: invalidated %s (idx=%d) - collides with pending mutation", fut.name, fut.idx)
			delete(s.futures, id)
		}
	}
}

// invalidateAllLocked drops every pending speculation. Caller holds mu.
func (s *streamSpeculator) invalidateAllLocked() {
	for id, fut := range s.futures {
		debug.Log("parallel", "stream-spec: invalidated %s (idx=%d) - tree-wide mutation", fut.name, fut.idx)
		delete(s.futures, id)
	}
}

// covers reports whether a speculative future exists for toolCalls index i,
// so the batch pre-executor can skip the duplicate.
func (s *streamSpeculator) covers(idx int) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, fut := range s.futures {
		if fut.idx == idx {
			return true
		}
	}
	return false
}

// collect resolves all pending futures into a preExecuted map keyed by tool
// call index. It waits for in-flight executions (bounded by preExecOne's own
// 30s timeout). Idempotent: only the first call returns results.
func (s *streamSpeculator) collect() map[int]preExecutedResult {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	s.done = true
	futures := make([]*specFuture, 0, len(s.futures))
	for _, fut := range s.futures {
		futures = append(futures, fut)
	}
	s.futures = make(map[string]*specFuture)
	s.mu.Unlock()

	var results map[int]preExecutedResult
	for _, fut := range futures {
		out := <-fut.ch
		if !out.ok {
			continue // failed speculation: sequential loop re-executes
		}
		if results == nil {
			results = make(map[int]preExecutedResult)
		}
		results[fut.idx] = out.res
	}
	s.cancel() // release any straggler
	if len(results) > 0 {
		debug.Log("parallel", "stream-spec: committed %d/%d speculations", len(results), len(futures))
	}
	return results
}
