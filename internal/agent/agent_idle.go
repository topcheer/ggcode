package agent

import (
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// IdleMaintainer implements sleep-time compute (r373, Letta/Berkeley
// arXiv 2504.13171): use user-idle windows to do maintenance work that
// would otherwise add latency or token cost to the next interaction.
//
// The maintainer watches two signals:
//   - lastActivity: bumped by Touch()/RunBegin()/RunEnd() (wired from
//     the agent run loop), and
//   - inflight: the number of agent runs in progress (a long run must
//     never be mistaken for idleness even past the threshold).
//
// After `after` of continuous idleness it fires ONCE per idle period
// (until the next Touch resets the period): when the context usage ratio
// is at/above ratioFloor it kicks a pre-compact, moving the compaction
// LLM call into the idle window instead of the next user message's
// critical path. StartPreCompact is snapshot-based and reentry-guarded,
// so firing is safe at any run boundary - and we only ever fire between
// runs (inflight == 0), the same boundary consumeReadyPreCompact uses.
type IdleMaintainer struct {
	after      time.Duration
	ratioFloor float64
	ratioFn    func() float64
	action     func()

	interval     time.Duration
	lastActivity atomic.Int64 // unix nanos
	inflight     atomic.Int32
	fired        atomic.Bool // one fire per idle period
	stopped      atomic.Bool
}

// NewIdleMaintainer builds a maintainer. ratioFn may be nil (treated as
// "never enough context"); action may be nil (idle period consumed, no
// work). Tests override interval after construction.
func NewIdleMaintainer(after time.Duration, ratioFloor float64, ratioFn func() float64, action func()) *IdleMaintainer {
	if after <= 0 {
		after = 10 * time.Minute
	}
	if ratioFn == nil {
		ratioFn = func() float64 { return 0 }
	}
	m := &IdleMaintainer{
		after:      after,
		ratioFloor: ratioFloor,
		ratioFn:    ratioFn,
		action:     action,
		interval:   30 * time.Second,
	}
	m.lastActivity.Store(time.Now().UnixNano())
	return m
}

// Touch records user activity and re-arms the idle period.
func (m *IdleMaintainer) Touch() {
	if m == nil {
		return
	}
	m.lastActivity.Store(time.Now().UnixNano())
	m.fired.Store(false)
}

// RunBegin marks an agent run in progress (also counts as activity).
func (m *IdleMaintainer) RunBegin() {
	if m == nil {
		return
	}
	m.inflight.Add(1)
	m.Touch()
}

// RunEnd closes one agent run.
func (m *IdleMaintainer) RunEnd() {
	if m == nil {
		return
	}
	m.inflight.Add(-1)
	m.Touch()
}

// Start launches the idle watcher goroutine.
func (m *IdleMaintainer) Start() {
	if m == nil {
		return
	}
	safego.Go("idle-maintainer", func() {
		tick := time.NewTicker(m.interval)
		defer tick.Stop()
		for {
			if m.stopped.Load() {
				return
			}
			m.check()
			<-tick.C
		}
	})
}

// Stop terminates the watcher loop.
func (m *IdleMaintainer) Stop() {
	if m == nil {
		return
	}
	m.stopped.Store(true)
}

func (m *IdleMaintainer) check() {
	if m.inflight.Load() > 0 {
		m.Touch() // a run in flight is activity, not idleness
		return
	}
	last := time.Unix(0, m.lastActivity.Load())
	if time.Since(last) < m.after {
		return
	}
	// One fire per idle period: CAS prevents every tick from re-firing.
	if !m.fired.CompareAndSwap(false, true) {
		return
	}
	ratio := m.ratioFn()
	if ratio < m.ratioFloor {
		debug.Log("idle", "idle window reached but ratio %.2f < floor %.2f; nothing to pre-compact", ratio, m.ratioFloor)
		return
	}
	if m.action == nil {
		return
	}
	debug.Log("idle", "idle window reached (ratio %.2f >= %.2f); starting pre-compact in the background", ratio, m.ratioFloor)
	m.action()
}
