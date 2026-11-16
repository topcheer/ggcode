package subagent

import (
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// IdleReport is the one-shot wake-up report a child sub-agent delivers to
// its parent when it enters a terminal (idle) state (#5700). Exactly one
// report is queued per child, at its first terminal transition; the report
// is dequeued exactly once by whichever consumer wins the drain (the
// parent's in-run completion feed or the idle wake), so consecutive idles
// never double-wake the parent.
type IdleReport struct {
	ID      string
	Name    string
	Status  Status
	Summary string
	At      time.Time
}

const (
	// maxIdleReports bounds the pending idle-report queue (#5700
	// multi-child backpressure). Overflow drops the newest report and
	// counts it; the child's full result stays retrievable via
	// wait_agent/list_agents polling, which is the documented fallback.
	maxIdleReports = 64

	// idleReportSummaryRunes bounds the per-report output summary carried
	// in the queue; full outputs are harvested via wait_agent.
	idleReportSummaryRunes = 400
)

// enqueueIdleReport records sa's terminal transition into the idle-report
// queue. First-transition-wins: the per-agent flag under sa.mu makes
// double-Complete, Complete-after-Cancel and watchdog races enqueue at most
// one report per child. Caller must not hold sa.mu or idleMu.
func (m *Manager) enqueueIdleReport(sa *SubAgent) {
	sa.mu.Lock()
	if sa.idleReportSent {
		sa.mu.Unlock()
		return
	}
	sa.idleReportSent = true
	rep := IdleReport{
		ID:     sa.ID,
		Name:   sa.Name,
		Status: sa.Status,
		At:     time.Now(),
	}
	switch {
	case sa.Result != "":
		rep.Summary = util.Truncate(sa.Result, idleReportSummaryRunes)
	case sa.Error != nil:
		rep.Summary = util.Truncate(sa.Error.Error(), idleReportSummaryRunes)
	}
	sa.mu.Unlock()

	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	if m.idleSilenced {
		debug.Log("subagent", "idle-report silenced (parent cancelled), dropping report for %s (poll via wait_agent)", rep.ID)
		return
	}
	if len(m.idleQueue) >= maxIdleReports {
		m.idleDropped++
		debug.Log("subagent", "idle-report queue full (%d): dropping report for %s (poll via wait_agent)", maxIdleReports, rep.ID)
		return
	}
	m.idleQueue = append(m.idleQueue, rep)
	select {
	case m.idleWake <- struct{}{}:
	default:
	}
}

// refreshIdleReport backfills a queued report's summary when a late result
// arrives for an agent whose report was enqueued with an empty summary
// (Complete's backfill path). No-op when no report is queued for the agent.
func (m *Manager) refreshIdleReport(sa *SubAgent) {
	sa.mu.Lock()
	id, summary := sa.ID, sa.Result
	sa.mu.Unlock()
	if summary == "" {
		return
	}
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	for i := range m.idleQueue {
		if m.idleQueue[i].ID == id && m.idleQueue[i].Summary == "" {
			m.idleQueue[i].Summary = util.Truncate(summary, idleReportSummaryRunes)
			return
		}
	}
}

func (m *Manager) consumeWakeLocked() {
	select {
	case <-m.idleWake:
	default:
	}
}

// DrainIdleReports atomically drains every pending report (consume-once:
// drained reports are never returned again) and clears the wake edge.
// This is the in-run delivery path: the parent's completion feed drains
// the queue at each conversation-loop iteration boundary, so reports
// enqueued mid-turn are delivered in-turn instead of being lost (#5700).
func (m *Manager) DrainIdleReports() []IdleReport {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	m.consumeWakeLocked()
	if len(m.idleQueue) == 0 {
		return nil
	}
	out := m.idleQueue
	m.idleQueue = nil
	return out
}

// DrainIdleReportsSince consumes only reports enqueued after cutoff and
// keeps older ones queued. The idle wake uses this split so reports that
// were already pending when the parent's previous run ended (delivered by
// the next run's in-run feed) do not self-wake a freshly finished parent,
// while reports arriving while the parent is idle wake it exactly once
// with a batched digest (#5700 finished-parent silence).
func (m *Manager) DrainIdleReportsSince(cutoff time.Time) []IdleReport {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	m.consumeWakeLocked()
	var fresh []IdleReport
	kept := m.idleQueue[:0]
	for _, r := range m.idleQueue {
		if r.At.After(cutoff) {
			fresh = append(fresh, r)
		} else {
			kept = append(kept, r)
		}
	}
	m.idleQueue = kept
	return fresh
}

// IdleWakeCh exposes the edge-triggered wake signal (capacity 1) for
// channel-based consumers. Signalled on enqueue; consumed by any drain.
func (m *Manager) IdleWakeCh() <-chan struct{} {
	return m.idleWake
}

// ConsumeIdleWake clears a pending wake edge without dropping queued
// reports. Called when a parent run ends: reports already queued keep
// their in-run delivery slot at the next run's iteration boundary, but
// they must not fire the idle wake themselves.
func (m *Manager) ConsumeIdleWake() {
	m.idleMu.Lock()
	m.consumeWakeLocked()
	m.idleMu.Unlock()
}

// SilenceIdleReports drops every pending report and disarms the queue.
// Used when the parent explicitly cancels its run: pending reports are
// silenced (the opencode interrupted-parent self-wake defect), and
// reports arising from the cancel's own CancelAll are dropped too. Full
// results remain retrievable via wait_agent/list_agents polling. The
// queue re-arms on the next user-initiated submission (ArmIdleReports).
func (m *Manager) SilenceIdleReports() {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	m.idleSilenced = true
	dropped := len(m.idleQueue)
	m.idleQueue = nil
	m.consumeWakeLocked()
	if dropped > 0 {
		debug.Log("subagent", "idle-reports silenced: dropped %d pending report(s) (poll via wait_agent)", dropped)
	}
}

// ArmIdleReports re-enables enqueueing after SilenceIdleReports. Wired to
// the parent's next user-initiated turn so a cancelled run never leaves
// the wake permanently disarmed.
func (m *Manager) ArmIdleReports() {
	m.idleMu.Lock()
	m.idleSilenced = false
	m.idleMu.Unlock()
}

// IdleReportStats reports the queue depth and the number of reports
// dropped by the bound (#5700 backpressure observability).
func (m *Manager) IdleReportStats() (pending int, dropped int) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	return len(m.idleQueue), m.idleDropped
}
