package agent

import (
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

type IdleMaintainer struct {
	after      time.Duration
	ratioFloor float64
	ratioFn    func() float64
	action     func()

	maintenance atomic.Value

	interval     time.Duration
	lastActivity atomic.Int64
	inflight     atomic.Int32
	fired        atomic.Bool
	stopped      atomic.Bool

	// maxSinceRun caps time since the last consolidation sweep: a busy
	// resident session (TUI/daemon with continuous traffic) keeps resetting
	// the idle clock and would otherwise NEVER consolidate - memory only
	// got swept at startup. When this cap elapses, check() fires once even
	// while busy-ish (still never with a run in flight).
	maxSinceRun time.Duration
	lastRun     atomic.Int64
}

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
		// Busy-resident cap (see field comment): sweep at least once per 6h
		// wall clock regardless of activity; never shorter than the idle gate
		// itself so a configured-short gate still defines the quiet-period
		// cadence and the cap only acts as the busy backstop.
		maxSinceRun: 6 * time.Hour,
	}
	if m.maxSinceRun < m.after {
		m.maxSinceRun = m.after
	}
	m.lastActivity.Store(time.Now().UnixNano())
	m.lastRun.Store(time.Now().UnixNano())
	return m
}

var maintenanceCleared = func() {}

func (m *IdleMaintainer) SetMaintenance(fn func()) {
	if m == nil {
		return
	}
	if fn == nil {
		fn = maintenanceCleared
	}
	m.maintenance.Store(fn)
}

func (m *IdleMaintainer) Touch() {
	if m == nil {
		return
	}
	m.lastActivity.Store(time.Now().UnixNano())
	m.fired.Store(false)
}

func (m *IdleMaintainer) RunBegin() {
	if m == nil {
		return
	}
	m.inflight.Add(1)
	m.Touch()
}

func (m *IdleMaintainer) RunEnd() {
	if m == nil {
		return
	}
	m.inflight.Add(-1)
	m.Touch()
}

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

func (m *IdleMaintainer) Stop() {
	if m == nil {
		return
	}
	m.stopped.Store(true)
}

func (m *IdleMaintainer) check() {
	if m.inflight.Load() > 0 {
		m.Touch()
		return
	}
	last := time.Unix(0, m.lastActivity.Load())
	idleReached := time.Since(last) >= m.after
	// Busy backstop: continuous activity resets the idle clock forever; the
	// cap fires anyway (once per elapsed cap window) so consolidation still
	// happens in long-lived resident sessions.
	lastRun := time.Unix(0, m.lastRun.Load())
	capReached := m.maxSinceRun > 0 && time.Since(lastRun) >= m.maxSinceRun
	if !idleReached && !capReached {
		return
	}
	if capReached && !idleReached {
		debug.Log("idle", "busy-resident cap reached (%.0fmin since last sweep); consolidating anyway", time.Since(lastRun).Minutes())
	}

	if !m.fired.CompareAndSwap(false, true) {
		return
	}
	defer m.lastRun.Store(time.Now().UnixNano())

	if fn, ok := m.maintenance.Load().(func()); ok && fn != nil {
		func() {
			defer safego.Recover("idle-maintenance")
			fn()
		}()
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
