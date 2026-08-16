package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCommandJobManagerLifecycle(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(context.Background(), "printf 'one\\ntwo\\n'", false, 5*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}
	if started == nil || started.ID == "" {
		t.Fatal("expected command job id")
	}

	snap, err := mgr.Wait(context.Background(), started.ID, 2*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait command job: %v", err)
	}
	if snap.Status != CommandJobCompleted {
		t.Fatalf("expected completed status, got %s", snap.Status)
	}
	if snap.TotalLines != 2 {
		t.Fatalf("expected 2 lines, got %d", snap.TotalLines)
	}
	if len(snap.Lines) != 2 || snap.Lines[0] != "one" || snap.Lines[1] != "two" {
		t.Fatalf("unexpected output lines: %#v", snap.Lines)
	}
}

func TestCommandJobManagerStop(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(context.Background(), "sleep 5", false, 30*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	if _, err := mgr.Stop(started.ID); err != nil {
		t.Fatalf("stop command job: %v", err)
	}

	snap, err := mgr.Wait(context.Background(), started.ID, 2*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait stopped command job: %v", err)
	}
	if snap.Status != CommandJobCancelled {
		t.Fatalf("expected cancelled status, got %s", snap.Status)
	}
}

func TestCommandJobManagerOwnerContextCancellationDoesNotStopJob(t *testing.T) {
	// After the fix, start_command uses context.Background() internally so
	// that cancelling the caller's context does NOT kill the background
	// process. The process must be stopped explicitly via Stop().
	ctx, cancel := context.WithCancel(context.Background())
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(ctx, "sleep 5", false, 30*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	cancel()

	// Give a brief moment for any potential (now absent) cancellation to propagate.
	time.Sleep(100 * time.Millisecond)

	// The process should still be running.
	snap, err := mgr.Read(started.ID, 10, 0)
	if err != nil {
		t.Fatalf("read command job: %v", err)
	}
	if snap.Status != CommandJobRunning {
		t.Fatalf("expected running status after owner context cancel, got %s", snap.Status)
	}

	// Clean up: stop the job explicitly.
	if _, err := mgr.Stop(started.ID); err != nil {
		t.Fatalf("stop command job: %v", err)
	}
}

func TestCommandJobManagerWaitHonoursCancelledContext(t *testing.T) {
	// #513: waitForCommandJob honors caller cancellation. A cancelled
	// context returns ctx.Err() immediately instead of waiting out the
	// poll window (the old discard-ctx behavior was an API trap).
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "sleep 5", false, 30*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()

	snap, err := mgr.Wait(waitCtx, started.ID, 1*time.Second, 20, 0)
	if err == nil {
		t.Fatalf("expected context error on cancelled context, got snapshot: %+v", snap)
	}

	if _, err := mgr.Stop(started.ID); err != nil {
		t.Fatalf("stop command job: %v", err)
	}
}

func TestCommandJobManagerWriteInput(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(context.Background(), "read line; printf 'echo:%s\\n' \"$line\"", false, 5*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	if _, err := mgr.Write(started.ID, "hello async", true); err != nil {
		t.Fatalf("write command input: %v", err)
	}

	snap, err := mgr.Wait(context.Background(), started.ID, 2*time.Second, 20, 0)
	if err != nil {
		t.Fatalf("wait command job: %v", err)
	}
	if snap.Status != CommandJobCompleted {
		t.Fatalf("expected completed status, got %s", snap.Status)
	}
	if len(snap.Lines) != 1 || snap.Lines[0] != "echo:hello async" {
		t.Fatalf("unexpected output lines: %#v", snap.Lines)
	}
}

func TestCommandJobManagerWriteInputFailsForStoppedJob(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())

	started, err := mgr.Start(context.Background(), "printf 'done\\n'", false, 5*time.Second)
	if err != nil {
		t.Fatalf("start command job: %v", err)
	}

	if _, err := mgr.Wait(context.Background(), started.ID, 2*time.Second, 20, 0); err != nil {
		t.Fatalf("wait command job: %v", err)
	}
	if _, err := mgr.Write(started.ID, "late input", true); err == nil {
		t.Fatal("expected write to completed command job to fail")
	}
}

func TestCommandJobToolDescriptionsGuideInteractiveUse(t *testing.T) {
	start := StartCommandTool{}
	for _, want := range []string{"long-running, streaming, or interactive", "Prefer run_command for quick one-shot", "write_command_input for stdin"} {
		if !strings.Contains(start.Description(), want) {
			t.Fatalf("start_command description should mention %q, got %q", want, start.Description())
		}
	}
	if !strings.Contains(string(start.Parameters()), "# ") {
		t.Fatalf("start_command schema should guide quick commands, got %s", string(start.Parameters()))
	}

	writeInput := WriteCommandInputTool{}
	if !strings.Contains(writeInput.Description(), "does not start a new command") {
		t.Fatalf("write_command_input description should clarify stdin-only behavior, got %q", writeInput.Description())
	}
	if !strings.Contains(string(writeInput.Parameters()), "not a new shell command") {
		t.Fatalf("write_command_input schema should clarify stdin-only behavior, got %s", string(writeInput.Parameters()))
	}
}

// TestSecondsToDurationClampsOverflow (#513 Bug1): the seconds→Duration
// multiplication must never overflow int64 — 9223372037s wraps negative
// (WithTimeout expires instantly) and 18446744074s wraps to ~290ms.
func TestSecondsToDurationClampsOverflow(t *testing.T) {
	tests := []struct {
		name    string
		seconds int
		want    time.Duration
	}{
		{"negative_uses_fallback", -1, 30 * time.Second},
		{"zero_uses_fallback", 0, 30 * time.Second},
		{"normal_value", 60, 60 * time.Second},
		{"overflow_negative_wrap", 9223372037, 86400 * time.Second},
		{"overflow_positive_wrap", 18446744074, 86400 * time.Second},
		{"absurd_value", 99999999999, 86400 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := secondsToDuration(tt.seconds, 30*time.Second)
			if got != tt.want {
				t.Fatalf("secondsToDuration(%d) = %v, want %v", tt.seconds, got, tt.want)
			}
		})
	}
}

// TestSnapshotTotalLinesExcludesPartial (#513 Bug2): the trailing partial
// line must not occupy a line number — when the next chunk completes it,
// since_line polling must return the merged line instead of [].
func TestSnapshotTotalLinesExcludesPartial(t *testing.T) {
	mgr := NewCommandJobManager(t.TempDir())
	started, err := mgr.Start(context.Background(), "printf 'line1\\npartial-without-newline'; sleep 0.3; printf -- '-continued\\n'", false, 10*time.Second)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Poll until the first chunk (partial, no trailing newline) is buffered.
	var snap1 *CommandJobSnapshot
	for i := 0; i < 50; i++ {
		s, err := mgr.Read(started.ID, 10, 0)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if s.TotalLines > 0 || (len(s.Lines) > 0 && strings.Contains(s.Lines[len(s.Lines)-1], "partial-without-newline")) {
			snap1 = &s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if snap1 == nil {
		t.Fatal("never observed the partial-line snapshot")
	}
	// The partial line is visible as a preview but must NOT be counted.
	if snap1.TotalLines != 1 {
		t.Fatalf("snapshot with trailing partial: TotalLines = %d, want 1 (completed lines only)", snap1.TotalLines)
	}

	// Wait for the second chunk to complete the merge.
	var snap2 *CommandJobSnapshot
	for i := 0; i < 100; i++ {
		s, err := mgr.Read(started.ID, 10, 0)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if s.TotalLines >= 2 {
			snap2 = &s
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if snap2 == nil {
		t.Fatal("never observed the merged-line snapshot")
	}
	if snap2.TotalLines != 2 {
		t.Fatalf("after merge: TotalLines = %d, want 2", snap2.TotalLines)
	}

	// The since_line=1 poll must now return the merged complete line
	// (the old bug returned [] — the merged line occupied a line number
	// already burned by the partial preview).
	s, err := mgr.Read(started.ID, 10, 1)
	if err != nil {
		t.Fatalf("read since: %v", err)
	}
	found := false
	for _, l := range s.Lines {
		if strings.Contains(l, "partial-without-newline-continued") {
			found = true
		}
	}
	if !found {
		t.Fatalf("since_line=1 polling must return the merged line, got %q", s.Lines)
	}

	if _, err := mgr.Stop(started.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
}
