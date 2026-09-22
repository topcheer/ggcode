package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// Tests for the detector effectiveness ledger (detector_ledger.go): the
// run-scoped per-tag accounting of guidance deliveries and budget
// suppressions that closes the empirical feedback loop on the ~40 heuristic
// behavior detectors.

func TestDetectorLedgerRecordsOutcomesByReason(t *testing.T) {
	var l detectorLedger
	l.setTurn(5)
	l.noteDelivered("[edit-oscillation]", 100)
	l.setTurn(9)
	l.noteDelivered("[edit-oscillation]", 50)
	l.noteSuppressed("[edit-oscillation]", rejectDedup)
	l.noteSuppressed("[spec-gaming]", rejectBudgetBytes)
	l.noteDelivered("", 10) // untagged buckets separately

	rows := l.snapshot()
	if len(rows) != 3 {
		t.Fatalf("snapshot rows = %d, want 3: %+v", len(rows), rows)
	}
	// Ordering: delivered desc, then total suppressed desc, then tag.
	if rows[0].Tag != "[edit-oscillation]" {
		t.Errorf("rows[0].Tag = %q, want [edit-oscillation]", rows[0].Tag)
	}
	eo := rows[0]
	if eo.Delivered != 2 || eo.Bytes != 150 {
		t.Errorf("edit-oscillation delivered/bytes = %d/%d, want 2/150", eo.Delivered, eo.Bytes)
	}
	if eo.FirstTurn != 5 || eo.LastTurn != 9 {
		t.Errorf("edit-oscillation span = %d-%d, want 5-9", eo.FirstTurn, eo.LastTurn)
	}
	if eo.SuppressedDedup != 1 || eo.TotalSuppressed() != 1 {
		t.Errorf("edit-oscillation dedup/total suppressed = %d/%d, want 1/1", eo.SuppressedDedup, eo.TotalSuppressed())
	}
	var untagged detectorLedgerRow
	for _, r := range rows {
		if r.Tag == detectorLedgerUntagged {
			untagged = r
		}
	}
	if untagged.Delivered != 1 || untagged.FirstTurn != 9 {
		t.Errorf("untagged row = %+v, want delivered=1 turn=9", untagged)
	}

	rep := l.report()
	for _, want := range []string{"[edit-oscillation]", "delivered=2", "dedup=1", "turns 5-9", detectorLedgerUntagged} {
		if !strings.Contains(rep, want) {
			t.Errorf("report missing %q:\n%s", want, rep)
		}
	}

	l.reset()
	if rows := l.snapshot(); len(rows) != 0 {
		t.Errorf("snapshot after reset = %d rows, want 0", len(rows))
	}
	if l.report() != "" {
		t.Errorf("report after reset = %q, want empty", l.report())
	}
}

func TestDetectorLedgerSnapshotOrdering(t *testing.T) {
	var l detectorLedger
	l.noteDelivered("[b-tag]", 10)
	l.noteDelivered("[a-tag]", 10)
	l.noteSuppressed("[z-tag]", rejectBudgetCount) // 0 delivered, suppressed only
	l.noteDelivered("[b-tag]", 10)

	rows := l.snapshot()
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Tag != "[b-tag]" || rows[1].Tag != "[a-tag]" || rows[2].Tag != "[z-tag]" {
		t.Errorf("ordering = %q,%q,%q; want b,a,z (delivered desc, then suppressed desc)", rows[0].Tag, rows[1].Tag, rows[2].Tag)
	}
}

func TestDetectorLedgerNilReceiverSafe(t *testing.T) {
	var l *detectorLedger
	l.setTurn(1)
	l.noteDelivered("[x]", 1)
	l.noteSuppressed("[x]", rejectDedup)
	l.reset()
	if rows := l.snapshot(); rows != nil {
		t.Errorf("nil ledger snapshot = %+v, want nil", rows)
	}
	if rep := l.report(); rep != "" {
		t.Errorf("nil ledger report = %q, want empty", rep)
	}
	l.logRunSummary() // must not panic
}

// Regression: logRunSummary previously called report() while holding l.mu;
// report() → snapshot() re-locked the same non-reentrant mutex and hung the
// run exit path forever (blocked TestRunStreamCancellationStopsRemaining-
// ToolCalls's canceled-run return). A regression re-deadlocks this test and
// fails via the package timeout.
func TestDetectorLedgerLogRunSummaryNoDeadlock(t *testing.T) {
	var l detectorLedger
	l.logRunSummary() // empty ledger: no-op
	l.setTurn(3)
	l.noteDelivered("[spec-gaming]", 100)
	l.noteSuppressed("[spec-gaming]", rejectBudgetBytes)
	l.logRunSummary() // populated ledger: must not self-deadlock
	if l.report() == "" {
		t.Fatal("expected non-empty report for populated ledger")
	}
}

func TestGuidanceBudgetLedgerWiring(t *testing.T) {
	var led detectorLedger
	g := &guidanceBudget{ledger: &led}

	mk := func(tag, body string) string { return "[" + tag + "] " + body }

	if !g.allowDeduped(mk("foo", "hello")) {
		t.Fatal("first foo hint should deliver")
	}
	if g.allowDeduped(mk("foo", "hello again")) {
		t.Fatal("duplicate foo hint should be deduped")
	}
	if !g.allowDeduped(mk("bar", "other")) {
		t.Fatal("bar hint should deliver")
	}
	// Count cap (guidanceBudgetPerTurn=2) reached: next advisory rejected.
	if g.allowDeduped(mk("baz", "third")) {
		t.Fatal("baz hint should hit the count cap")
	}
	// Critical bypasses the count cap but charges the critical pool.
	if !g.allowDeduped(mk("git-destructive", "force push detected")) {
		t.Fatal("critical hint should bypass count cap")
	}
	// Advisory byte cap: 2048-byte pool already partly free here; push a
	// huge untagged advisory through inject-path allow() to exhaust it.
	g.injected = guidanceBudgetPerTurn // force count-cap path off; use bytes
	big := strings.Repeat("x", guidanceBudgetBytesPerTurn)
	if g.allow(big) {
		t.Fatal("oversized advisory should hit the byte cap")
	}

	rows := led.snapshot()
	byTag := map[string]detectorLedgerRow{}
	for _, r := range rows {
		byTag[r.Tag] = r
	}
	if r := byTag["foo"]; r.Delivered != 1 || r.SuppressedDedup != 1 {
		t.Errorf("foo row = %+v, want delivered=1 dedup=1", r)
	}
	if r := byTag["bar"]; r.Delivered != 1 {
		t.Errorf("bar row = %+v, want delivered=1", r)
	}
	if r := byTag["baz"]; r.SuppressedCount != 1 {
		t.Errorf("baz row = %+v, want count-suppressed=1", r)
	}
	if r := byTag["git-destructive"]; r.Delivered != 1 {
		t.Errorf("critical row = %+v, want delivered=1", r)
	}
	if r := byTag[detectorLedgerUntagged]; r.SuppressedBytes != 1 {
		t.Errorf("untagged row = %+v, want byte-suppressed=1", r)
	}
}

func TestAgentInjectGuidanceLedgerIntegration(t *testing.T) {
	a := NewAgent(nil, tool.NewRegistry(), "", 5)

	msg := func(tag string) string { return "[" + tag + "] please " + tag }
	if !a.injectGuidance(msg("alpha")) || !a.injectGuidance(msg("beta")) {
		t.Fatal("first two injections should deliver within budget")
	}
	if a.injectGuidance(msg("gamma")) {
		t.Fatal("third injection should be suppressed by count cap")
	}

	rows := a.GuidanceLedgerSnapshot()
	if len(rows) != 3 {
		t.Fatalf("snapshot rows = %d, want 3", len(rows))
	}
	if rows[0].Tag != "alpha" || rows[0].Delivered != 1 {
		t.Errorf("rows[0] = %+v, want alpha delivered=1", rows[0])
	}
	gamma := rows[2]
	if gamma.Tag != "gamma" || gamma.TotalSuppressed() != 1 {
		t.Errorf("gamma row = %+v, want suppressed=1", gamma)
	}
	rep := a.GuidanceLedgerReport()
	if !strings.Contains(rep, "[gamma]") || !strings.Contains(rep, "count=1") {
		t.Errorf("report missing gamma count suppression:\n%s", rep)
	}

	// Run-start reset gives every run a clean ledger.
	a.detectorLedger.reset()
	if rows := a.GuidanceLedgerSnapshot(); len(rows) != 0 {
		t.Errorf("snapshot after run reset = %d rows, want 0", len(rows))
	}
}
