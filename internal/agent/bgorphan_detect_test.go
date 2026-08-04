package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBgOrphan_NoWarnWhenNoBgCommands(t *testing.T) {
	s := newBgOrphanState()
	if msg := s.checkOrphanedCommands(5); msg != "" {
		t.Fatalf("expected empty when no bg commands, got: %s", msg)
	}
}

func TestBgOrphan_NoWarnImmediatelyAfterStart(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev", "description": "dev server"}`)
	s.recordStartCommand(args, result, 1)

	// Same iteration -- should not warn
	if msg := s.checkOrphanedCommands(1); msg != "" {
		t.Fatalf("expected no warning at same iteration, got: %s", msg)
	}

	// One iteration later -- still under threshold (bgOrphanThreshold=2)
	if msg := s.checkOrphanedCommands(2); msg != "" {
		t.Fatalf("expected no warning 1 iteration later, got: %s", msg)
	}
}

func TestBgOrphan_WarnsAfterThreshold(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev", "description": "dev server"}`)
	s.recordStartCommand(args, result, 1)

	// 3 iterations later (gap=2, meets threshold)
	msg := s.checkOrphanedCommands(3)
	if msg == "" {
		t.Fatal("expected orphan warning after threshold iterations")
	}
	if !strings.Contains(msg, "job-1") {
		t.Errorf("expected job_id in warning, got: %s", msg)
	}
	if !strings.Contains(msg, "npm run dev") {
		t.Errorf("expected command in warning, got: %s", msg)
	}
	if !strings.Contains(msg, "read_command_output") {
		t.Errorf("expected tool suggestion in warning, got: %s", msg)
	}
}

func TestBgOrphan_NoWarnAfterOutputCheck(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev"}`)
	s.recordStartCommand(args, result, 1)

	// Agent reads output at iteration 2 -- resets the timer
	readArgs := json.RawMessage(`{"job_id": "job-1"}`)
	s.recordOutputCheck(readArgs, 2)

	// At iteration 3, gap is only 1 -- should not warn
	if msg := s.checkOrphanedCommands(3); msg != "" {
		t.Fatalf("expected no warning after recent output check, got: %s", msg)
	}
}

func TestBgOrphan_StopCommandClearsTracking(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev"}`)
	s.recordStartCommand(args, result, 1)

	// Stop the command
	stopArgs := json.RawMessage(`{"job_id": "job-1"}`)
	s.recordStopCommand(stopArgs)

	// Should not warn since job was stopped
	if msg := s.checkOrphanedCommands(5); msg != "" {
		t.Fatalf("expected no warning after stop, got: %s", msg)
	}
}

func TestBgOrphan_MaxInjections(t *testing.T) {
	s := newBgOrphanState()

	// Start multiple jobs
	for i := 0; i < 5; i++ {
		result := `{"job_id": "job-` + string(rune('1'+i)) + `", "status": "started"}`
		args := json.RawMessage(`{"command": "echo test"}`)
		s.recordStartCommand(args, result, 1)
	}

	// Each check can produce one warning per unique job, but total injections capped at 3
	warnCount := 0
	for iter := 3; iter <= 10; iter++ {
		if msg := s.checkOrphanedCommands(iter); msg != "" {
			warnCount++
		}
	}
	if warnCount > bgOrphanMaxInjections {
		t.Errorf("expected max %d injections, got %d", bgOrphanMaxInjections, warnCount)
	}
}

func TestBgOrphan_MultipleOrphans(t *testing.T) {
	s := newBgOrphanState()

	result1 := `{"job_id": "job-a", "status": "started"}`
	args1 := json.RawMessage(`{"command": "npm run dev"}`)
	s.recordStartCommand(args1, result1, 1)

	result2 := `{"job_id": "job-b", "status": "started"}`
	args2 := json.RawMessage(`{"command": "go run main.go"}`)
	s.recordStartCommand(args2, result2, 1)

	msg := s.checkOrphanedCommands(4)
	if msg == "" {
		t.Fatal("expected orphan warning")
	}
	if !strings.Contains(msg, "2 background commands") {
		t.Errorf("expected plural message, got: %s", msg)
	}
}

func TestBgOrphan_NoRepeatWarningForSameJob(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev"}`)
	s.recordStartCommand(args, result, 1)

	// First warning
	msg1 := s.checkOrphanedCommands(3)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second check at later iteration -- should not warn again for same job
	// (but could if we haven't hit max injections yet)
	msg2 := s.checkOrphanedCommands(5)
	if msg2 != "" {
		t.Fatalf("expected no repeat warning for same job, got: %s", msg2)
	}
}

func TestBgOrphan_ReWarnAfterRecheck(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev"}`)
	s.recordStartCommand(args, result, 1)

	// First warning
	if msg := s.checkOrphanedCommands(3); msg == "" {
		t.Fatal("expected first warning")
	}

	// Agent rechecks output -- clears warning state
	readArgs := json.RawMessage(`{"job_id": "job-1"}`)
	s.recordOutputCheck(readArgs, 3)

	// Goes stale again
	msg := s.checkOrphanedCommands(6)
	if msg == "" {
		t.Fatal("expected re-warning after going stale again")
	}
}

func TestBgOrphan_Reset(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev"}`)
	s.recordStartCommand(args, result, 1)

	s.reset()

	if len(s.activeJobs) != 0 {
		t.Errorf("expected empty jobs after reset, got %d", len(s.activeJobs))
	}
	if msg := s.checkOrphanedCommands(5); msg != "" {
		t.Fatalf("expected no warning after reset, got: %s", msg)
	}
}

func TestBgOrphan_ExtractJobID(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   string
	}{
		{"JSON format", `{"job_id": "abc123", "status": "running"}`, "abc123"},
		{"Job ID label", `Started background command. Job ID: xyz789`, "xyz789"},
		{"No job id", `Command failed: not found`, ""},
		{"Empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJobID(tt.result)
			if got != tt.want {
				t.Errorf("extractJobID(%q) = %q, want %q", tt.result, got, tt.want)
			}
		})
	}
}

func TestBgOrphan_ParseStartCommandArgs(t *testing.T) {
	args := json.RawMessage(`{"command": "# start dev server\necho hi", "description": "test"}`)
	desc, cmd := parseStartCommandArgs(args)
	if desc != "test" {
		t.Errorf("desc = %q, want 'test'", desc)
	}
	if !strings.Contains(cmd, "echo hi") {
		t.Errorf("cmd = %q, expected to contain 'echo hi'", cmd)
	}
}

func TestBgOrphan_TruncateBgCmd(t *testing.T) {
	short := "echo hello"
	if got := truncateBgCmd(short, 100); got != short {
		t.Errorf("truncateBgCmd short = %q, want %q", got, short)
	}

	long := strings.Repeat("a", 200)
	got := truncateBgCmd(long, 50)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncated string to end with ..., got: %q", got)
	}
	if len(got) != 50 {
		t.Errorf("expected length 50, got %d", len(got))
	}

	// Comment stripping
	withComment := "# my description\necho hello"
	if got := truncateBgCmd(withComment, 100); got != "echo hello" {
		t.Errorf("expected comment stripped, got: %q", got)
	}
}
