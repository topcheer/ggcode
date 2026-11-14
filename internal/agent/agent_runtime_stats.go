package agent

import (
	"sync"
	"time"
)

// #5635 P2: live introspection truth sources for the a2a status and
// inflight faces. The status face previously reported only
// busy/model/since/spinner; RuntimeStatus carries Goal/Iteration/
// TokensIn/TokensOut/ContextPct that were never filled. These small
// snapshot blocks are written from the conversation loop hot path and
// read from the introspection RPC goroutine, so each carries its own
// mutex - deliberately NOT the agent-wide a.mu, to keep the hot path
// free of that lock's ordering constraints.

// RuntimeStatsSnapshot is a point-in-time view of the live run counters
// reported by the a2a status face.
type RuntimeStatsSnapshot struct {
	Iterations int     // current run's iteration (1-based, 0 while idle)
	TokensIn   int64   // current run's cumulative input tokens
	TokensOut  int64   // current run's cumulative output tokens
	ContextPct float64 // contextManager.TokenCount / promptBudget, clamped 0..100
	Goal       string  // current autopilot goal ("" when not in autopilot)
}

// runtimeStats accumulates the current run's counters. beginRun resets
// at the start of each run so the face always describes the live (or
// most recent) run, matching runStats.Iterations semantics.
type runtimeStats struct {
	mu         sync.Mutex
	iterations int
	tokensIn   int64
	tokensOut  int64
}

func (rs *runtimeStats) beginRun() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.iterations = 0
	rs.tokensIn = 0
	rs.tokensOut = 0
}

func (rs *runtimeStats) setIteration(n int) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.iterations = n
}

func (rs *runtimeStats) addTokens(in, out int64) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.tokensIn += in
	rs.tokensOut += out
}

// CurrentToolCallSnapshot describes the main agent's in-flight tool
// call; Tool is empty when the loop is not executing a tool.
type CurrentToolCallSnapshot struct {
	Tool       string
	ParamsHead string
	StartedAt  time.Time
}

// currentToolCall tracks which tool the conversation loop is executing
// right now (inflight face truth source; sub-agent inflight previously
// worked, main-agent had none).
type currentToolCall struct {
	mu         sync.Mutex
	tool       string
	paramsHead string
	startedAt  time.Time
}

func (c *currentToolCall) begin(tool, args string) {
	const headLimit = 120
	head := args
	if len(head) > headLimit {
		head = head[:headLimit]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tool = tool
	c.paramsHead = head
	c.startedAt = time.Now()
}

func (c *currentToolCall) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tool = ""
	c.paramsHead = ""
	c.startedAt = time.Time{}
}

// RuntimeStats returns the live snapshot behind the a2a status face
// (#5635 P2). Token totals and iteration describe the current (or most
// recently finished) run; ContextPct is the context manager's fill
// ratio against the prompt budget, clamped to 0..100.
func (a *Agent) RuntimeStats() RuntimeStatsSnapshot {
	var snap RuntimeStatsSnapshot
	a.runtimeStats.mu.Lock()
	snap.Iterations = a.runtimeStats.iterations
	snap.TokensIn = a.runtimeStats.tokensIn
	snap.TokensOut = a.runtimeStats.tokensOut
	a.runtimeStats.mu.Unlock()

	a.mu.RLock()
	snap.Goal = a.autopilotGoal
	a.mu.RUnlock()

	if a.contextManager != nil {
		tokens := a.contextManager.TokenCount()
		if budget := a.promptBudget(); budget > 0 {
			pct := float64(tokens) / float64(budget) * 100
			if pct < 0 {
				pct = 0
			}
			if pct > 100 {
				pct = 100
			}
			snap.ContextPct = pct
		}
	}
	return snap
}

// CurrentTool returns the main agent's in-flight tool call (#5635 P2
// inflight face). Tool is "" while the agent is between tool calls or
// not running.
func (a *Agent) CurrentTool() CurrentToolCallSnapshot {
	a.currentTool.mu.Lock()
	defer a.currentTool.mu.Unlock()
	return CurrentToolCallSnapshot{
		Tool:       a.currentTool.tool,
		ParamsHead: a.currentTool.paramsHead,
		StartedAt:  a.currentTool.startedAt,
	}
}
