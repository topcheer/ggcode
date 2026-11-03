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
	}
	m.lastActivity.Store(time.Now().UnixNano())
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
	if time.Since(last) < m.after {
		return
	}

	if !m.fired.CompareAndSwap(false, true) {
		return
	}

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
