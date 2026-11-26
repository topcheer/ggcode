package agent

import "sync"

// errorBudget is an SLO-style reliability statistic: a sliding window of tool
// outcomes compared against a success-rate SLO. It used to inject two
// steering messages ("fast burn" warning and a "BUDGET EXHAUSTED ... ask the
// user" breaker); those were removed per user decision - the model decides
// when to stop retrying, not a counter. The window and burn rate stay as a
// read-only observation (snapshot / introspection), nothing is ever returned
// to the conversation.
type errorBudget struct {
	mu     sync.Mutex
	slo    float64 // target success rate (0..1)
	window int     // sliding window size
	ring   []bool  // true = success
	head   int
	filled int
}

func newErrorBudget() *errorBudget {
	return &errorBudget{slo: 0.85, window: 20}
}

func (e *errorBudget) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ring = nil
	e.head, e.filled = 0, 0
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

// evaluate records the outcome. It never returns guidance: the former
// fast-burn warning and breaker injection are gone (the AI decides when to
// stop). Kept as the single recording entry point for the watcher.
func (e *errorBudget) evaluate(success bool) string {
	e.record(success)
	return ""
}
