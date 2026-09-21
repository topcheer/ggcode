package agent

import (
	"runtime"
	"runtime/debug"
	"sync"
)

// Adaptive width control for parallel read-only pre-execution.
//
// The previous policy was static: parallelMaxConcurrent (3, the W&D optimum,
// arXiv:2602.07359) narrowed only by context-fill. That is one environment
// signal out of several that matter. Frontier practice for agent runtimes is
// adaptive concurrency driven by overload signals rather than static caps:
//
//   - "Backpressure in Agent Pipelines" (tianpan.co, 2026-04-12,
//     https://tianpan.co/blog/2026/04/12/backpressure-in-agent-pipelines-when-ai-generates-work-faster-than-it-can-execute):
//     bounded queues + circuit breakers + adaptive concurrency beat static
//     caps when the environment degrades.
//   - "Backpressure and Concurrency Caps for AI Agents" (negiadventures,
//     https://negiadventures.github.io/blog/backpressure-concurrency-ai-agents):
//     react to overload signals instead of amplifying latency into
//     self-inflicted outages.
//   - AgentCgroup (arXiv:2602.09345): runtime-adaptive resource policies
//     aligned with tool-call boundaries.
//
// This controller combines three signals into one width decision per batch:
//
//  1. Context fill (existing behavior, unchanged thresholds): a batch of
//     large results landing at once can trigger compaction.
//  2. Heap pressure vs GOMEMLIMIT: on memory-constrained machines, N
//     concurrent reads/greps/semantic searches fan memory out exactly when
//     the host can least afford it.
//  3. EWMA of recent pre-exec failure ratio: when read-only tools keep
//     failing (LSP not ready, index cold, fs hiccups), pre-executed work is
//     wasted and the sequential loop re-runs it — pure overhead. A sustained
//     failure rate narrows, then skips, pre-exec; clean batches let it
//     recover.

const (
	// memPressureSkip: heap ≥ 85% of GOMEMLIMIT → skip pre-exec entirely.
	memPressureSkip = 0.85
	// memPressureNarrow: heap ≥ 70% of GOMEMLIMIT → width 1.
	memPressureNarrow = 0.70
	// errEMANarrow / errEMASkip: EWMA of per-batch pre-exec failure ratio.
	errEMANarrow = 0.50
	errEMASkip   = 0.80
	// emaAlpha: EWMA smoothing factor over batch failure ratios.
	emaAlpha = 0.30
	// emaClean: below this the EWMA snaps to 0 so a clean streak recovers
	// the full width instead of decaying asymptotically.
	emaClean = 0.01
)

// adaptiveWidth holds process-wide adaptive state for pre-exec width.
// Package-level (not an Agent field) so every agent in the process shares
// one view of environment pressure — resource signals are host-wide anyway.
type adaptiveWidth struct {
	mu     sync.Mutex
	errEMA float64
}

// preExecWidthCtl is the process-wide controller. Tests call
// resetPreExecWidth to isolate state.
var preExecWidthCtl = &adaptiveWidth{}

// memPressureRatio returns heap bytes as a fraction of the current Go memory
// limit, or ~0 when no meaningful limit is set. Overridable in tests.
// (debug.SetMemoryLimit(-1) reads without modifying; when GOMEMLIMIT is
// unset the limit is MaxInt64 and the ratio is effectively zero.)
var memPressureRatio = func() float64 {
	limit := debug.SetMemoryLimit(-1)
	if limit <= 0 {
		return 0
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapAlloc) / float64(limit)
}

// cpuWidthCap caps width so small machines don't oversubscribe: one core is
// kept for the agent loop, GC and provider I/O; larger machines keep the W&D
// width of 3. Overridable in tests.
var cpuWidthCap = func() int {
	n := runtime.NumCPU()
	switch {
	case n <= 2:
		return 1
	case n == 3:
		return 2
	default:
		return parallelMaxConcurrent
	}
}

// recordBatch folds one batch's outcome into the failure EWMA. Only real
// execution failures count; context cancellation and missing-tool lookups
// are excluded by the caller (they are not environment degradation).
func (c *adaptiveWidth) recordBatch(executed, failed int) {
	total := executed + failed
	if total <= 0 {
		return
	}
	ratio := float64(failed) / float64(total)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errEMA = emaAlpha*ratio + (1-emaAlpha)*c.errEMA
	if c.errEMA < emaClean {
		c.errEMA = 0
	}
}

// ema reports the current failure EWMA (for logging).
func (c *adaptiveWidth) ema() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.errEMA
}

// width returns the allowed pre-exec width for the next batch; 0 means skip
// pre-execution entirely (the sequential loop still runs every call).
// ctxFill < 0 means the context fill ratio is unknown (no context manager).
func (c *adaptiveWidth) width(ctxFill float64) int {
	if ctxFill >= contextFillCritical {
		return 0
	}
	w := cpuWidthCap()
	if ctxFill >= contextFillHigh {
		w = 1
	}
	if m := memPressureRatio(); m >= memPressureSkip {
		return 0
	} else if m >= memPressureNarrow {
		w = 1
	}
	switch ema := c.ema(); {
	case ema >= errEMASkip:
		return 0
	case ema >= errEMANarrow:
		w = 1
	}
	return w
}

// resetPreExecWidth restores a clean controller (test isolation).
func resetPreExecWidth() {
	preExecWidthCtl = &adaptiveWidth{}
}
