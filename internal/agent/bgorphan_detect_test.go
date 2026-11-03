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

	if msg := s.checkOrphanedCommands(1); msg != "" {
		t.Fatalf("expected no warning at same iteration, got: %s", msg)
	}

	if msg := s.checkOrphanedCommands(2); msg != "" {
		t.Fatalf("expected no warning 1 iteration later, got: %s", msg)
	}
}

func TestBgOrphan_WarnsAfterThreshold(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev", "description": "dev server"}`)
	s.recordStartCommand(args, result, 1)

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

	readArgs := json.RawMessage(`{"job_id": "job-1"}`)
	s.recordOutputCheck(readArgs, "Status: running\nTotal lines: 1", 2)

	if msg := s.checkOrphanedCommands(3); msg != "" {
		t.Fatalf("expected no warning after recent output check, got: %s", msg)
	}
}

func TestBgOrphan_StopCommandClearsTracking(t *testing.T) {
	s := newBgOrphanState()
	result := `{"job_id": "job-1", "status": "started"}`
	args := json.RawMessage(`{"command": "npm run dev"}`)
	s.recordStartCommand(args, result, 1)

	stopArgs := json.RawMessage(`{"job_id": "job-1"}`)
	s.recordStopCommand(stopArgs)

	if msg := s.checkOrphanedCommands(5); msg != "" {
		t.Fatalf("expected no warning after stop, got: %s", msg)
	}
}

func TestBgOrphan_MaxInjections(t *testing.T) {
	s := newBgOrphanState()

	for i := 0; i < 5; i++ {
		result := `{"job_id": "job-` + string(rune('1'+i)) + `", "status": "started"}`
		args := json.RawMessage(`{"command": "echo test"}`)
		s.recordStartCommand(args, result, 1)
	}

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

	msg1 := s.checkOrphanedCommands(3)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

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

	if msg := s.checkOrphanedCommands(3); msg == "" {
		t.Fatal("expected first warning")
	}

	readArgs := json.RawMessage(`{"job_id": "job-1"}`)
	s.recordOutputCheck(readArgs, "Status: running\nTotal lines: 2", 3)

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

	withComment := "# my description\necho hello"
	if got := truncateBgCmd(withComment, 100); got != "echo hello" {
		t.Errorf("expected comment stripped, got: %q", got)
	}
}

func TestBgOrphanTerminalStatusExitsTracking(t *testing.T) {
	s := newBgOrphanState()

	s.recordStartCommand(json.RawMessage("{\"command\":\"sleep 1\"}"), "Job ID: job-1\nStatus: running", 1)
	if len(s.activeJobs) == 0 {
		t.Fatal("job not tracked after start")
	}
	readArgs := json.RawMessage(`{"job_id": "job-1"}`)
	s.recordOutputCheck(readArgs, "Job ID: job-1\nStatus: completed\nTotal lines: 3", 2)
	if len(s.activeJobs) != 0 {
		t.Fatalf("completed job still tracked: %d", len(s.activeJobs))
	}
}

func TestBGOrphanEvictedJobReadStopsCycle(t *testing.T) {
	s := newBgOrphanState()
	s.mu.Lock()
	s.activeJobs["job-9"] = &bgJobInfo{JobID: "job-9", Command: "sleep 100", StartIter: 1}
	s.mu.Unlock()
	args := json.RawMessage(`{"job_id":"job-9"}`)
	s.recordOutputCheck(args, "Error: command job \"job-9\" not found", 5)
	s.mu.Lock()
	_, still := s.activeJobs["job-9"]
	s.mu.Unlock()
	if still {
		t.Fatal("evicted (not found) job must be removed from tracking")
	}
}

func TestBGOrphanQuotedNotFoundOutputKeepsTracking(t *testing.T) {
	s := newBgOrphanState()
	s.mu.Lock()
	s.activeJobs["job-7"] = &bgJobInfo{JobID: "job-7", Command: "go test ./internal/tool/", StartIter: 1}
	s.mu.Unlock()
	args := json.RawMessage(`{"job_id":"job-7"}`)

	quoted := "=== RUN TestCommandJobs\n--- FAIL: TestCommandJobs\ncommand job \"job-9\" not found\nFAIL\nStatus: running"
	s.recordOutputCheck(args, quoted, 5)
	s.mu.Lock()
	_, still := s.activeJobs["job-7"]
	s.mu.Unlock()
	if !still {
		t.Fatal("running job quoting the phrase in stdout must stay tracked")
	}

	s.recordOutputCheck(args, "Error: command job \"job-7\" not found", 7)
	s.mu.Lock()
	_, still = s.activeJobs["job-7"]
	s.mu.Unlock()
	if still {
		t.Fatal("error-channel not-found must remove the job from tracking")
	}
}

func TestBgOrphanEchoedStatusDoesNotRetire1712(t *testing.T) {
	s := newBgOrphanState()
	s.recordStartCommand(json.RawMessage("{\"command\":\"make test\"}"), "Job ID: job-9\nStatus: running", 1)
	if len(s.activeJobs) == 0 {
		t.Fatal("job not tracked after start")
	}

	s.recordOutputCheck(json.RawMessage(`{"job_id": "job-9"}`), "=== RUN TestX\nStatus: completed (cached)\n--- PASS", 2)
	if len(s.activeJobs) != 1 {
		t.Fatalf("echoed status must not retire tracking, tracked=%d", len(s.activeJobs))
	}

	s.recordOutputCheck(json.RawMessage(`{"job_id": "job-9"}`), "Job ID: job-9\nStatus: completed", 3)
	if len(s.activeJobs) != 0 {
		t.Fatal("manager status block must retire tracking")
	}
}
