package agent

import (
	"fmt"
	"sync"
)

// errorBudget is an SLO-style reliability budget: a sliding window of tool
// outcomes compared against a success-rate SLO. Fast burn injects a warning;
// exhausting the budget trips the breaker, which injects an escalation
// prompt telling the agent to stop retrying blindly and ask for a human
// decision. It complements compounding_failure (single-shot hint layer):
// this is the action-oriented budget layer (research round #28, G1/G2).
type errorBudget struct {
	mu        sync.Mutex
	slo       float64 // target success rate (0..1)
	window    int     // sliding window size
	ring      []bool  // true = success
	head      int
	filled    int
	tripped   bool
	warnFired bool
}

func newErrorBudget() *errorBudget {
	return &errorBudget{slo: 0.85, window: 20}
}

func (e *errorBudget) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ring = nil
	e.head, e.filled = 0, 0
	e.tripped = false
	e.warnFired = false
}

func (e *errorBudget) record(success bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.ring) < e.window {
		e.ring = append(e.ring, success)
		return
	}
	e.ring[e.head] = success
	e.head = (e.head + 1) % e.window
}

// burnRate is the current failure rate inside the window (0 when empty).
func (e *errorBudget) burnRate() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.filled == 0 && len(e.ring) == 0 {
		return 0
	}
	fails := 0
	for _, ok := range e.ring {
		if !ok {
			fails++
		}
	}
	return float64(fails) / float64(len(e.ring))
}

// Evaluate records the outcome and returns guidance text when the budget is
// burning fast (once) or exhausted (trip). Empty string = stay quiet.
func (e *errorBudget) evaluate(success bool) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.ring) < e.window {
		e.ring = append(e.ring, success)
	} else {
		e.ring[e.head] = success
		e.head = (e.head + 1) % e.window
	}

	if e.tripped {
		return "" // tripped guidance already injected; stay quiet afterwards
	}
	fails := 0
	for _, ok := range e.ring {
		if !ok {
			fails++
		}
	}
	n := len(e.ring)
	failRate := float64(fails) / float64(n)

	// Fast burn: failure rate double the nominal budget while the window is
	// full (needs >=8 samples to be meaningful).
	if n >= 8 && !e.warnFired && failRate > 2*(1-e.slo) {
		e.warnFired = true
		return fmt.Sprintf("[error-budget] fast burn: %d/%d recent tool calls failed (rate %.0f%% vs %.0f%% budget). Re-read the failing tool's output and change approach before the next attempt.", fails, n, failRate*100, (1-e.slo)*100)
	}

	// Budget exhausted: trip the breaker.
	if n >= e.window/2 && failRate > 1-e.slo && fails >= 6 {
		e.tripped = true
		return fmt.Sprintf("[error-budget] BUDGET EXHAUSTED: %d/%d recent tool calls failed (SLO %.0f%%). Stop retrying the same approach. Summarize what you tried, then ask the user (ask_user) for a decision before issuing more tool calls.", fails, n, e.slo*100)
	}
	return ""
}
