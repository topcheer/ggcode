package tool

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1118: the command-job ring buffer bounded only line COUNT
// (maxCommandLogLines), not bytes. A single multi-MB line was stored whole by
// addLineLocked and streamed back intact through snapshot /
// commandSnapshotOutput / formatCommandJobSnapshot. These tests pin the new
// byte caps: per-line at ingestion, total at the snapshot/format layer.

func bigLine(n int) string {
	// prefix/suffix lets tests assert head+tail preservation around truncation.
	return "HEAD-" + strings.Repeat("x", n) + "-TAIL"
}

// TestIssue1118_PerLineCapAtIngestion covers addLineLocked: a single huge line
// must be truncated before it is stored, with a recognizable marker.
func TestIssue1118_PerLineCapAtIngestion(t *testing.T) {
	m := NewCommandJobManager(t.TempDir())
	job := m.newJob("cat minified.json", time.Minute, func() {})

	line := bigLine(5 << 20) // 5MB single line, exactly the reported case
	job.appendOutput(line + "\n")

	snap := m.snapshot(job)
	if len(snap.Lines) != 1 {
		t.Fatalf("expected 1 buffered line, got %d", len(snap.Lines))
	}
	got := snap.Lines[0]
	if !strings.Contains(got, "[truncated") {
		t.Fatalf("stored line missing recognizable truncation marker")
	}
	if !strings.HasPrefix(got, "HEAD-") || !strings.HasSuffix(got, "-TAIL") {
		t.Fatalf("head/tail not preserved: prefix=%q suffix=%q", got[:20], got[len(got)-20:])
	}
	if max := maxCommandLineBytes * 2; len(got) > max {
		t.Fatalf("stored line too large: %d bytes > %d cap(2x)", len(got), max)
	}
}

// TestIssue1118_PartialLineCappedWhileRunning covers appendOutput's partial
// path: an unterminated dump must stay bounded while the job is still running.
func TestIssue1118_PartialLineCappedWhileRunning(t *testing.T) {
	m := NewCommandJobManager(t.TempDir())
	job := m.newJob("stream-no-newline", time.Minute, func() {})

	job.appendOutput(bigLine(3 << 20)) // no trailing newline -> lands in partial

	snap := m.snapshot(job)
	for i, l := range snap.Lines {
		if max := maxCommandLineBytes * 2; len(l) > max {
			t.Fatalf("snapshot line %d too large: %d bytes > %d", i, len(l), max)
		}
	}
	if n := len(snap.Lines); n != 1 {
		t.Fatalf("expected partial exposed as 1 line, got %d", n)
	}
	if !strings.Contains(snap.Lines[0], "[truncated") {
		t.Fatalf("partial line missing truncation marker")
	}
}

// TestIssue1118_TotalCap_CommandSnapshotOutput covers the auto-background
// return path in run_command.go: many per-line-capped lines still sum to well
// over any sane budget, so commandSnapshotOutput must apply a total ceiling.
func TestIssue1118_TotalCap_CommandSnapshotOutput(t *testing.T) {
	snap := CommandJobSnapshot{
		ID:     "cmd-cap",
		Status: CommandJobCompleted,
		Lines:  make([]string, 0, maxCommandLogLines),
	}
	for i := 0; i < maxCommandLogLines; i++ {
		snap.Lines = append(snap.Lines, fmt.Sprintf("line %04d %s", i, strings.Repeat("y", 4096)))
	}
	out := commandSnapshotOutput(snap)
	if out == "" {
		t.Fatal("expected output")
	}
	if max := maxCommandSnapshotBytes + 4096; len(out) > max {
		t.Fatalf("commandSnapshotOutput returned %d bytes, exceeds %d", len(out), max)
	}
	if !strings.Contains(out, "omitted") {
		t.Fatalf("total-cap truncation marker missing from output")
	}
	if !strings.HasSuffix(out, snap.Lines[maxCommandLogLines-1]) {
		t.Fatalf("tail of output must be preserved (truncateMiddle semantics)")
	}
}

// TestIssue1118_TotalCap_FormatCommandJobSnapshot covers the read_command_output /
// wait_command formatting layer used by production tools.
func TestIssue1118_TotalCap_FormatCommandJobSnapshot(t *testing.T) {
	snap := CommandJobSnapshot{
		ID:           "cmd-fmt",
		Command:      "make verify-ci",
		Status:       CommandJobFailed,
		Duration:     time.Second,
		BufferedFrom: 1,
		ErrText:      "exit status 1",
		Lines:        make([]string, 0, maxCommandLogLines),
	}
	for i := 0; i < maxCommandLogLines; i++ {
		snap.Lines = append(snap.Lines, strings.Repeat("z", 4096))
	}
	out := formatCommandJobSnapshot(snap, true)
	if max := maxCommandSnapshotBytes + 4096; len(out) > max {
		t.Fatalf("formatCommandJobSnapshot returned %d bytes, exceeds %d", len(out), max)
	}
	if !strings.Contains(out, "omitted") {
		t.Fatalf("total-cap truncation marker missing from formatted snapshot")
	}
	if !strings.Contains(out, "exit status 1") {
		t.Fatalf("error text must survive the total cap")
	}
}

// TestIssue1118_LineCountSemanticsUnchanged pins that byte capping does not
// alter ring-buffer line-count behavior (#513-era semantics).
func TestIssue1118_LineCountSemanticsUnchanged(t *testing.T) {
	const overflow = maxCommandLogLines + 50
	m := NewCommandJobManager(t.TempDir())
	job := m.newJob("seq loop", time.Minute, func() {})

	var b strings.Builder
	for i := 1; i <= overflow; i++ {
		fmt.Fprintf(&b, "%06d|%s\n", i, strings.Repeat("q", 1024))
	}
	job.appendOutput(b.String())

	snap := m.snapshot(job)
	if snap.TotalLines != overflow {
		t.Fatalf("TotalLines = %d, want %d", snap.TotalLines, overflow)
	}
	wantFrom := overflow - maxCommandLogLines + 1
	if snap.BufferedFrom != wantFrom {
		t.Fatalf("BufferedFrom = %d, want %d", snap.BufferedFrom, wantFrom)
	}
	if len(snap.Lines) != maxCommandLogLines {
		t.Fatalf("buffered lines = %d, want %d", len(snap.Lines), maxCommandLogLines)
	}
	first := fmt.Sprintf("%06d|", wantFrom)
	if !strings.HasPrefix(snap.Lines[0], first) {
		t.Fatalf("oldest retained line should be %d, got prefix %q", wantFrom, snap.Lines[0][:12])
	}
}

// TestIssue1118_ReadAndSelectPreserveCaps covers manager.Read: selecting a
// tail of already-capped lines stays within the total budget end-to-end.
func TestIssue1118_ReadAndSelectPreserveCaps(t *testing.T) {
	m := NewCommandJobManager(t.TempDir())
	job := m.newJob("noisy build", time.Minute, func() {})

	var b strings.Builder
	for i := 0; i < maxCommandLogLines; i++ {
		b.WriteString(strings.Repeat("n", 4096) + "\n")
	}
	job.appendOutput(b.String())

	snap, err := m.Read(job.ID, 400, 0)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	out := formatCommandJobSnapshot(snap, true)
	if max := maxCommandSnapshotBytes + 4096; len(out) > max {
		t.Fatalf("Read-formatted output %d bytes exceeds %d", len(out), max)
	}
}
