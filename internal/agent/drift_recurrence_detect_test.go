package agent

import (
	"testing"
)

func TestDriftRecurrence_NoWarningNotFired(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(5)
	d.recordEdit("/project/src/main.go")
	d.recordEdit("/project/src/util.go")
	d.recordEdit("/project/src/helper.go")

	if msg := d.check(); msg != "" {
		t.Fatalf("expected no guidance without warning, got: %s", msg)
	}
}

func TestDriftRecurrence_TooFewEditsPostWarn(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(5)
	d.markWarning(3)
	d.recordEdit("/project/src/main.go")
	d.recordEdit("/project/src/util.go")

	if msg := d.check(); msg != "" {
		t.Fatalf("expected no guidance with too few post-warn edits, got: %s", msg)
	}
}

func TestDriftRecurrence_NewDirsExceedThreshold(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	d.recordEdit("/project/src/main.go") // pre-warn
	d.recordEdit("/project/src/util.go") // pre-warn

	d.markWarning(2)
	d.recordIteration(3)

	// Post-warning: edit files in 5 NEW directories
	d.recordEdit("/project/pkgA/file.go")
	d.recordEdit("/project/pkgB/file.go")
	d.recordEdit("/project/pkgC/file.go")
	d.recordEdit("/project/pkgD/file.go")
	d.recordEdit("/project/pkgE/file.go")

	msg := d.check()
	if msg == "" {
		t.Fatal("expected drift recurrence guidance, got empty")
	}
	if d.fired != true {
		t.Fatal("expected fired=true after recurrence detected")
	}
}

func TestDriftRecurrence_SameDirsNotRecurrence(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	// Pre-warn: edit in several directories
	d.recordEdit("/project/src/main.go")
	d.recordEdit("/project/src/util.go")
	d.recordEdit("/project/src/helper.go")
	d.recordEdit("/project/src/config.go")

	d.markWarning(2)
	d.recordIteration(3)

	// Post-warning: edit in SAME directories, plus one verification
	d.recordEdit("/project/src/main.go")
	d.recordEdit("/project/src/util.go")
	d.recordEdit("/project/src/helper.go")
	d.recordVerification()

	msg := d.check()
	if msg != "" {
		t.Fatalf("expected no recurrence for same-dir edits with verification, got: %s", msg)
	}
}

func TestDriftRecurrence_NoVerificationPostWarn(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	d.recordEdit("/project/src/main.go")
	d.recordEdit("/project/src/util.go")

	d.markWarning(2)
	d.recordIteration(3)

	// Post-warning: edits in same dirs but NO verification at all
	d.recordEdit("/project/src/main.go")
	d.recordEdit("/project/src/util.go")
	d.recordEdit("/project/src/helper.go")

	msg := d.check()
	if msg == "" {
		t.Fatal("expected drift recurrence guidance for zero-verification post-warn")
	}
}

func TestDriftRecurrence_FiresOnceOnly(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	d.markWarning(2)
	d.recordIteration(3)

	// Trigger recurrence
	d.recordEdit("/project/pkgA/file.go")
	d.recordEdit("/project/pkgB/file.go")
	d.recordEdit("/project/pkgC/file.go")
	d.recordEdit("/project/pkgD/file.go")

	msg1 := d.check()
	if msg1 == "" {
		t.Fatal("expected first guidance")
	}

	// Should NOT fire again
	msg2 := d.check()
	if msg2 != "" {
		t.Fatal("expected empty on second check (fires once)")
	}
}

func TestDriftRecurrence_PostWarnWindowExceeded(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	d.markWarning(2)
	// Move iteration past the window
	d.recordIteration(2 + driftRecurrencePostWarnWindow + 1)

	d.recordEdit("/project/pkgA/file.go")
	d.recordEdit("/project/pkgB/file.go")
	d.recordEdit("/project/pkgC/file.go")
	d.recordEdit("/project/pkgD/file.go")

	msg := d.check()
	if msg != "" {
		t.Fatalf("expected no guidance past post-warn window, got: %s", msg)
	}
}

func TestDriftRecurrence_Reset(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	d.markWarning(2)
	d.recordEdit("/project/src/main.go")
	d.reset()

	if d.warned != false {
		t.Fatal("expected warned=false after reset")
	}
	if len(d.preWarnDirs) != 0 {
		t.Fatal("expected empty preWarnDirs after reset")
	}
	if len(d.postWarnDirs) != 0 {
		t.Fatal("expected empty postWarnDirs after reset")
	}
	if d.fired != false {
		t.Fatal("expected fired=false after reset")
	}
}

func TestDriftRecurrence_PreWarnDirsTracked(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	d.recordEdit("/alpha/src/main.go")
	d.recordEdit("/beta/test/util.go")

	if len(d.preWarnDirs) != 2 {
		t.Fatalf("expected 2 pre-warn dirs, got %d", len(d.preWarnDirs))
	}
}

func TestDriftRecurrence_EmptyFilePathIgnored(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	d.recordEdit("")
	d.recordEdit("")

	if len(d.preWarnDirs) != 0 {
		t.Fatalf("expected 0 dirs for empty paths, got %d", len(d.preWarnDirs))
	}
}

func TestDriftRecurrence_VerificationOnlyPostWarn(t *testing.T) {
	d := newDriftRecurrenceState()
	d.recordIteration(1)
	d.markWarning(2)
	d.recordIteration(3)

	// Edits in same dirs + verification should NOT trigger
	d.recordEdit("/project/src/main.go")
	d.recordEdit("/project/src/main.go")
	d.recordEdit("/project/src/main.go")
	d.recordVerification()

	msg := d.check()
	if msg != "" {
		t.Fatalf("expected no recurrence with verification, got: %s", msg)
	}
}
