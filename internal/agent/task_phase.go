package agent

import (
	"strings"
	"sync"
)

// task_phase.go -- Unified Task-Phase Monitor (Metacognitive Control Layer)
//
// Research basis:
//   - arXiv:2601.01743 "AI Agent Systems: Architectures, Applications, and
//     Evaluation" (2026): the central engineering challenge for dependable
//     autonomy is "principled allocation of test-time compute under explicit
//     budgets", governed by a unified monitoring layer rather than scattered
//     per-module heuristics.
//   - Nelson & Narens (1990) metacognitive framework (also cited by
//     consensus.go): one meta-level monitor feeds several object-level
//     controllers, so every controller sees the same ground truth instead of
//     maintaining divergent private observations.
//   - "MetaCognition Patterns for AI Agent Self-Monitoring and Adaptive
//     Control" (zylos.ai, 2026-03-14): observation streams that feed multiple
//     controllers must share one observation pipeline; divergent private state
//     produces cross-controller contradictions.
//
// Before this file, adaptive_effort.go and adaptive_sampling.go each kept a
// private sliding window over the same tool-result stream (agent.go recorded
// every tool result twice, once per adapter). The private windows could
// disagree — and sampling's copy predated the #1436-A/#1836 error-filtering
// fixes, so innocuous read-only failures (grep miss, file-not-found) or
// routine old_text-not-found retries pushed the temperature to maximum
// determinism for subsequent turns while effort correctly ignored them.
//
// The unified monitor below is the single source of truth: one window, one
// record site, one canonical signal computation with the corrected error
// filter. adaptive_effort and adaptive_sampling become thin policies that map
// the shared signals to reasoning effort and sampling temperature respectively.

// taskPhaseWindowSize controls how many recent tool interactions the unified
// monitor retains. A small window keeps both downstream policies responsive
// to context shifts (exploration → editing → recovery) without over-weighting
// stale history. (Formerly adaptiveEffortWindow / adaptiveSamplingWindow;
// both were 6, so the unified size preserves prior behavior.)
const taskPhaseWindowSize = 6

// phaseSignals is the canonical window summary consumed by the per-adapter
// policies. Counts are mutually exclusive per entry: an error entry only
// increments RecentErrors (when it passes the recovery filter); a success is
// classified as exactly one of edit/creative/read-only.
type phaseSignals struct {
	Total         int // window length (successes + filtered-out errors)
	RecentErrors  int // source-mutating failures excluding param-format retries
	EditCount     int
	ReadOnlyCount int
	CreativeCount int
}

// taskPhaseWindow is the shared sliding window of tool results. All
// compute-allocation adapters (reasoning effort, sampling temperature) read
// from this single monitor instead of keeping private copies of the event
// stream.
type taskPhaseWindow struct {
	mu      sync.Mutex
	entries []effortEntry
}

func newTaskPhaseWindow() *taskPhaseWindow {
	return &taskPhaseWindow{}
}

// record appends a tool interaction, evicting entries beyond the window size.
// errText (a short prefix of a failed call's error output) enables the
// param-format retry filter; pass "" for successes.
func (w *taskPhaseWindow) record(toolName string, isError bool, errText string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	et := ""
	if isError {
		et = strings.ToLower(errText)
		if len(et) > 160 {
			et = et[:160]
		}
	}
	w.entries = append(w.entries, effortEntry{toolName: toolName, isError: isError, errText: et})
	if len(w.entries) > taskPhaseWindowSize {
		w.entries = w.entries[len(w.entries)-taskPhaseWindowSize:]
	}
}

// reset clears the window for a new user turn.
func (w *taskPhaseWindow) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = w.entries[:0]
}

// signals computes the canonical phase summary. Errors count toward
// RecentErrors only when they come from source-mutating tools and are not
// routine param-format retries (#1436-A: read-only misses must not poison
// recovery detection; #1836: old_text-not-found is retry material, not a
// recovery puzzle). Read-only failures are simply ignored. Every consumer of
// the unified monitor gets this corrected filter by construction — it can no
// longer drift between adapters.
func (w *taskPhaseWindow) signals() phaseSignals {
	w.mu.Lock()
	defer w.mu.Unlock()

	var s phaseSignals
	s.Total = len(w.entries)
	for _, e := range w.entries {
		if e.isError {
			if errorRecoverySignals[e.toolName] && !isParamFormatEditFailure(e.toolName, e.errText) {
				s.RecentErrors++
			}
			continue
		}
		switch {
		case editTools[e.toolName]:
			s.EditCount++
		case creativeTools[e.toolName]:
			s.CreativeCount++
		case effortReadOnlyTools[e.toolName]:
			s.ReadOnlyCount++
		}
	}
	return s
}
