package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Detector Effectiveness Ledger - per-detector empirical feedback loop.
//
// Research basis:
//   - TRAIL benchmark (Patronus AI, arXiv:2505.08638): 148 human-annotated
//     agentic traces with 841 errors show that detector quality must be
//     MEASURED, not assumed - the best LLM localizes only ~11% of trace
//     errors, so cheap heuristics carry the load and need precision data.
//   - Pisama agent-trace calibration (2026): 57 rule-based detectors reached
//     mean precision 0.81 only AFTER an internal calibration loop over 8k+
//     entries - the loop, not the thresholds, produced the quality.
//   - arXiv:2602.15391 "Hybrid Abstention and Adaptive Detection": static
//     heuristic thresholds yield ~25% false positives; adaptive calibration
//     driven by measured outcomes cuts that to ~3%.
//
// Problem: ggcode routes ALL budgeted detector guidance through exactly two
// gates (guidanceBudget.allow / allowDeduped), but after a run the only
// artifact is a single per-turn "suppressed" counter. Nobody can answer the
// calibration questions the literature says are prerequisite:
//   - which detectors actually delivered guidance this run, how often?
//   - which detectors were silently starved by the byte cap / count cap /
//     dedup / critical pool - i.e. firing state + paying tier sampling cost
//     for zero delivered context?
//   - how many bytes of context did each detector consume?
//
// Solution: a run-scoped ledger keyed by the hints' head tag
// ([edit-oscillation], [spec-gaming], ...). The two budget gates record
// delivered vs suppressed outcomes per tag with the suppression reason; the
// run loop stamps the current iteration so rows get first/last-turn spans.
// At run end the aggregate is logged (debug "detector-ledger") and exposed
// via GuidanceLedgerSnapshot for UI/export consumers.
//
// Scope note: this ledger deliberately measures the BUDGETED population only
// - one-shot notifications that bypass the budget (plan suggestions, loop
// recovery nudges, session-timeout warnings) have their own hard caps and
// are not part of the calibration question.

// detectorLedgerRow is one tag's cumulative run statistics.
type detectorLedgerRow struct {
	Tag                string
	Delivered          int // hints actually injected into context
	Bytes              int // delivered hint bytes
	SuppressedBytes    int // rejected: advisory byte pool exhausted
	SuppressedCount    int // rejected: per-turn count cap reached
	SuppressedDedup    int // rejected: same tag already delivered this turn
	SuppressedCritical int // rejected: critical byte pool exhausted
	FirstTurn          int // 1-based iteration of first activity; 0 = pre-loop
	LastTurn           int
}

// TotalSuppressed returns the row's total rejections across all reasons.
func (r detectorLedgerRow) TotalSuppressed() int {
	return r.SuppressedBytes + r.SuppressedCount + r.SuppressedDedup + r.SuppressedCritical
}

// detectorLedger accumulates per-tag guidance outcomes for one run.
// All methods are nil-receiver safe so direct guidanceBudget constructions
// (tests, alternate runtimes) keep working without wiring.
type detectorLedger struct {
	mu    sync.Mutex
	turn  int // current 1-based iteration; 0 before the loop starts
	stats map[string]*detectorLedgerRow
}

const detectorLedgerUntagged = "(untagged)"

// setTurn stamps the current 1-based iteration for subsequent records.
func (l *detectorLedger) setTurn(n int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.turn = n
}

// reset clears all per-run state (called once at run start).
func (l *detectorLedger) reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.turn = 0
	l.stats = nil
}

func (l *detectorLedger) rowFor(tag string, turn int) *detectorLedgerRow {
	if l.stats == nil {
		l.stats = make(map[string]*detectorLedgerRow)
	}
	r, ok := l.stats[tag]
	if !ok {
		r = &detectorLedgerRow{Tag: tag}
		l.stats[tag] = r
	}
	if turn > 0 {
		if r.FirstTurn == 0 {
			r.FirstTurn = turn
		}
		r.LastTurn = turn
	}
	return r
}

// noteDelivered records a hint that passed the budget gate and was injected.
func (l *detectorLedger) noteDelivered(tag string, nBytes int) {
	if l == nil {
		return
	}
	if tag == "" {
		tag = detectorLedgerUntagged
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.rowFor(tag, l.turn)
	r.Delivered++
	r.Bytes += nBytes
}

// noteSuppressed records a hint rejected by the budget gate with the reason.
func (l *detectorLedger) noteSuppressed(tag string, reason guidanceReject) {
	if l == nil || reason == rejectNone {
		return
	}
	if tag == "" {
		tag = detectorLedgerUntagged
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.rowFor(tag, l.turn)
	switch reason {
	case rejectBudgetBytes:
		r.SuppressedBytes++
	case rejectBudgetCount:
		r.SuppressedCount++
	case rejectDedup:
		r.SuppressedDedup++
	case rejectCriticalBytes:
		r.SuppressedCritical++
	}
}

// snapshot returns rows sorted by delivered desc, then suppressed desc,
// then tag asc - the "is this detector earning its keep" ordering.
func (l *detectorLedger) snapshot() []detectorLedgerRow {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotLocked()
}

// snapshotLocked is snapshot with the mutex already held (logRunSummary).
func (l *detectorLedger) snapshotLocked() []detectorLedgerRow {
	rows := make([]detectorLedgerRow, 0, len(l.stats))
	for _, r := range l.stats {
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Delivered != b.Delivered {
			return a.Delivered > b.Delivered
		}
		if ta, tb := a.TotalSuppressed(), b.TotalSuppressed(); ta != tb {
			return ta > tb
		}
		return a.Tag < b.Tag
	})
	return rows
}

// formatLedgerBytes renders a byte count compactly for ledger lines.
func formatLedgerBytes(n int) string {
	switch {
	case n >= 1024*10:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	case n >= 1024:
		return fmt.Sprintf("%.2fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// report renders the run's ledger as a compact multi-line summary.
// Empty ledger (no budgeted guidance activity) renders as "".
func (l *detectorLedger) report() string {
	if l == nil {
		return ""
	}
	return l.renderRows(l.snapshot())
}

// reportLocked renders with the mutex already held - logRunSummary must
// NOT call report() while holding l.mu: renderRows → snapshot() would
// re-Lock on the same non-reentrant goroutine mutex and hang the run's
// exit forever (caught by TestRunStreamCancellationStopsRemainingToolCalls,
// whose cancel-return path was blocked by the deadlock).
func (l *detectorLedger) reportLocked() string {
	return l.renderRows(l.snapshotLocked())
}

func (l *detectorLedger) renderRows(rows []detectorLedgerRow) string {
	if len(rows) == 0 {
		return ""
	}
	var delivered, suppressed, bytes int
	for _, r := range rows {
		delivered += r.Delivered
		suppressed += r.TotalSuppressed()
		bytes += r.Bytes
	}
	var b strings.Builder
	fmt.Fprintf(&b, "guidance ledger: %d tag(s), %d delivered (%s), %d suppressed",
		len(rows), delivered, formatLedgerBytes(bytes), suppressed)
	for _, r := range rows {
		span := ""
		if r.FirstTurn > 0 {
			if r.FirstTurn == r.LastTurn {
				span = fmt.Sprintf(" turn %d", r.FirstTurn)
			} else {
				span = fmt.Sprintf(" turns %d-%d", r.FirstTurn, r.LastTurn)
			}
		}
		fmt.Fprintf(&b, "\n  [%s] delivered=%d (%s) suppressed{bytes=%d count=%d dedup=%d crit=%d}%s",
			r.Tag, r.Delivered, formatLedgerBytes(r.Bytes),
			r.SuppressedBytes, r.SuppressedCount, r.SuppressedDedup, r.SuppressedCritical, span)
	}
	return b.String()
}

// logRunSummary logs the run-end ledger aggregate. Safe to defer: uses
// TryLock so a panic unwinding while a record call holds the mutex cannot
// deadlock the unwind (same-goroutine Go mutexes are non-reentrant).
func (l *detectorLedger) logRunSummary() {
	if l == nil {
		return
	}
	if !l.mu.TryLock() {
		return
	}
	defer l.mu.Unlock()
	if len(l.stats) == 0 {
		return
	}
	debug.Log("detector-ledger", "%s", l.reportLocked())
}

// GuidanceLedgerSnapshot returns the current run's per-tag guidance
// statistics, ordered by delivered volume.
func (a *Agent) GuidanceLedgerSnapshot() []detectorLedgerRow {
	return a.detectorLedger.snapshot()
}

// GuidanceLedgerReport renders the current run's ledger summary ("" if no
// budgeted guidance fired this run).
func (a *Agent) GuidanceLedgerReport() string {
	return a.detectorLedger.report()
}
