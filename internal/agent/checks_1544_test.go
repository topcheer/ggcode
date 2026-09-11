package agent

import "testing"

// recordEditFiltered mirrors driftRecurrenceRecord's ok-gating without an Agent.
func recordEditFilteredFor1544(d *driftRecurrenceState, tool, path string, ok bool) {
	if productiveEditTools[tool] && ok {
		d.recordEdit(path)
	}
}

// #1544 case B pin: a FAILED verification still counts as verification run.
func Test1544FailedVerificationCounts(t *testing.T) {
	d := newDriftRecurrenceState()
	d.markWarning(5)
	// red-phase go test that FAILED (ok=false)
	d.recordVerification()
	d.recordEdit("/w/src/a.go")
	if d.postWarnVerifies != 1 {
		t.Fatalf("failed verification must count, got %d", d.postWarnVerifies)
	}
	// failed edit must NOT count
	d2 := newDriftRecurrenceState()
	d2.markWarning(5)
	recordEditFilteredFor1544(d2, "edit_file", "/w/src/a.go", false)
	if d2.postWarnEdits != 0 {
		t.Fatalf("failed edit must not count, got %d", d2.postWarnEdits)
	}
}

// #1544 case C pin: weak (empty) pre-warn baseline -> the 2-3-new-dirs
// relaxed branch must not fire; fat baseline keeps it.
func Test1544WeakBaselineRaisesBar(t *testing.T) {
	weak := newDriftRecurrenceState()
	weak.markWarning(5) // baseline 0 (plan-drift early arm)
	for _, p := range []string{"/w/src/x.go", "/w/src/x_test.go"} {
		weak.recordEdit(p)
	}
	if msg := weak.check(); msg != "" {
		t.Fatalf("weak baseline + normal 2-dir workflow must not fire, got: %s", msg)
	}

	fat := newDriftRecurrenceState()
	for i := 0; i < 5; i++ {
		fat.recordEdit("/w/dir" + string(rune('a'+i)) + "/f.go")
	}
	fat.markWarning(5) // fat baseline
	fat.recordEdit("/w/new1/a.go")
	fat.recordEdit("/w/new2/b.go")
	for i := 0; i < 6; i++ {
		fat.recordEdit("/w/new1/a.go") // reach min edits
	}
	if msg := fat.check(); msg == "" {
		t.Fatal("fat baseline scattering with zero verifies must fire")
	}
	// #1452-A reconciliation: empty baseline with THREE scattered dirs and
	// zero verifies must still fire (converged-single-dir stays spared).
	d3 := newDriftRecurrenceState()
	d3.markWarning(5)
	d3.recordEdit("/a/x.go")
	d3.recordEdit("/b/y.go")
	d3.recordEdit("/c/z.go")
	for i := 0; i < 5; i++ {
		d3.recordEdit("/a/x.go")
	}
	if msg := d3.check(); msg == "" {
		t.Fatal("empty baseline 3-dir scattering with zero verifies must fire")
	}
}
