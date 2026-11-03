package agentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkRep(n int, successes int, winnerUsable, degraded bool) BestOfNReport {
	rep := BestOfNReport{N: n, WinnerIndex: -1, Degraded: degraded}
	for i := 0; i < n; i++ {
		st, vd := "failed", "failed"
		if i < successes {

			st, vd = "completed", "succeeded"
		}
		rep.Candidates = append(rep.Candidates, CandidateOutcome{
			Name: "c", ID: "id", Status: st, Verdict: vd,
		})
	}
	if winnerUsable {
		rep.WinnerIndex = 0
		rep.Candidates[0].Status = "completed"
		rep.Candidates[0].Verdict = "succeeded"
	}
	return rep
}

func mkRepCompletedOnly(n int, winnerUsable, degraded bool, partials int) BestOfNReport {
	rep := BestOfNReport{N: n, WinnerIndex: -1, Degraded: degraded}
	for i := 0; i < n; i++ {
		vd := "failed"
		if i < partials {
			vd = "partial"
		}
		rep.Candidates = append(rep.Candidates, CandidateOutcome{
			Name: "c", ID: "id", Status: "completed", Verdict: vd,
		})
	}
	if winnerUsable {
		rep.WinnerIndex = 0
	}
	return rep
}

func TestBONGapLedgerHeadroomHint(t *testing.T) {
	l := NewBONGapLedger("")

	for i := 0; i < 5; i++ {
		l.Record("patrol the repo", mkRep(3, 0, false, true))
	}
	hint, ok := l.AdmissionHint("patrol the repo")
	if !ok {
		t.Fatal("low-headroom history must produce an admission hint")
	}
	if !strings.Contains(hint, "headroom") {
		t.Fatalf("hint should name coverage headroom, got: %s", hint)
	}
}

func TestBONGapLedgerCompletedOnlyIsZeroHeadroom(t *testing.T) {
	l := NewBONGapLedger("")
	for i := 0; i < 5; i++ {
		l.Record("fix flaky test X", mkRepCompletedOnly(3, false, true, 1))
	}
	hint, ok := l.AdmissionHint("fix flaky test X")
	if !ok {
		t.Fatal("all-completed-none-succeeded history must produce a hint")
	}
	if !strings.Contains(hint, "headroom") {
		t.Fatalf("hint should name coverage headroom (generator pool exhausted), got: %s", hint)
	}
	if strings.Contains(hint, "verifier_models") {
		t.Fatalf("misdiagnosis (#4136): hint recommends selector fix but the generator pool has zero true successes: %s", hint)
	}
}

func TestBONGapLedgerSelectorHint(t *testing.T) {
	l := NewBONGapLedger("")

	for i := 0; i < 4; i++ {
		l.Record("fix flaky test", mkRep(2, 2, false, true))
	}
	l.Record("fix flaky test", mkRep(2, 2, true, false))
	hint, ok := l.AdmissionHint("fix flaky test")
	if !ok {
		t.Fatal("weak-selector history must produce an admission hint")
	}
	if !strings.Contains(hint, "verifier_models") {
		t.Fatalf("hint should recommend verifier_models, got: %s", hint)
	}
}

func TestBONGapLedgerHealthyHistorySilent(t *testing.T) {
	l := NewBONGapLedger("")
	for i := 0; i < 8; i++ {
		l.Record("clean task", mkRep(3, 3, true, false))
	}
	if hint, ok := l.AdmissionHint("clean task"); ok {
		t.Fatalf("healthy history must stay silent, got: %s", hint)
	}
}

func TestBONGapLedgerSampleGate(t *testing.T) {
	l := NewBONGapLedger("")
	for i := 0; i < bonLedgerMinSamples-1; i++ {
		l.Record("sparse task", mkRep(2, 0, false, true))
	}
	if _, ok := l.AdmissionHint("sparse task"); ok {
		t.Fatal("below the minimum sample count the ledger must not speak")
	}

	if _, ok := l.AdmissionHint("different task entirely"); ok {
		t.Fatal("unrelated task must have no history")
	}
}

func TestBONGapLedgerWindowTrim(t *testing.T) {
	l := NewBONGapLedger("")

	for i := 0; i < bonLedgerWindow; i++ {
		l.Record("recovering task", mkRep(2, 0, false, true))
	}
	if _, ok := l.AdmissionHint("recovering task"); !ok {
		t.Fatal("pre-trim history should hint")
	}
	for i := 0; i < bonLedgerWindow; i++ {
		l.Record("recovering task", mkRep(2, 2, true, false))
	}
	if hint, ok := l.AdmissionHint("recovering task"); ok {
		t.Fatalf("post-trim healthy window must be silent, got: %s", hint)
	}
}

func TestBONGapLedgerSkipsPartialAndErrored(t *testing.T) {
	l := NewBONGapLedger("")
	partial := mkRep(3, 3, true, false)
	partial.Partial = true
	l.Record("t", partial)
	errored := mkRep(3, 3, true, false)
	errored.Err = "launch failure"
	l.Record("t", errored)
	if _, ok := l.AdmissionHint("t"); ok {
		t.Fatal("partial/errored runs carry no admission signal and must not be recorded")
	}
}

func TestBONGapLedgerPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "bon-gap-ledger.json")
	l1 := NewBONGapLedger(path)
	for i := 0; i < bonLedgerMinSamples; i++ {
		l1.Record("persisted task", mkRep(3, 0, false, true))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ledger must persist after record: %v", err)
	}
	l2 := NewBONGapLedger(path)
	if _, ok := l2.AdmissionHint("persisted task"); !ok {
		t.Fatal("reopened ledger must retain the recorded history")
	}
}

func TestBONGapLedgerCorruptStateQuarantined(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bon-gap-ledger.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewBONGapLedger(path)
	if _, ok := l.AdmissionHint("anything"); ok {
		t.Fatal("corrupt state must behave as an empty ledger")
	}
}

func TestBONGapLedgerNilAndTestModeSafe(t *testing.T) {
	var nilLedger *BONGapLedger
	nilLedger.Record("t", mkRep(2, 2, true, false))
	if _, ok := nilLedger.AdmissionHint("t"); ok {
		t.Fatal("nil ledger must not speak")
	}

	RecordBestOfNGap("t", mkRep(2, 2, true, false))
	if _, ok := BestOfNAdmissionHint("t"); ok {
		t.Fatal("test mode must not consult or mutate real user state")
	}
}

func TestBONTaskFingerprintStableAndBinning(t *testing.T) {
	a := bonTaskFingerprint("Patrol   the repo\nfor regressions ")
	b := bonTaskFingerprint("patrol the repo for regressions")
	if a != b {
		t.Fatal("whitespace/case churn must not shard one recurring task")
	}
	if bonTaskFingerprint("completely different") == a {
		t.Fatal("different tasks must bin differently")
	}
}

func TestBestOfNReportRendersAdmissionHint(t *testing.T) {
	rep := mkRep(2, 2, true, false)
	rep.AdmissionHint = "ensemble admission: history says stop"
	out := formatBestOfNReport(rep, "succeeded")
	if !strings.Contains(out, "Note: ensemble admission: history says stop") {
		t.Fatalf("admission hint must render as a trailing Note, got: %s", out)
	}

	rep2 := mkRep(2, 2, true, false)
	if out2 := formatBestOfNReport(rep2, "succeeded"); strings.Contains(out2, "Note:") {
		t.Fatalf("no-hint report must not contain a Note line, got: %s", out2)
	}
}
