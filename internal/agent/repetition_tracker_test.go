package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRepetitionTracker_FailedEditSoftHint(t *testing.T) {
	rt := newRepetitionTracker()
	args := json.RawMessage(`{"file_path":"main.go"}`)

	// First 2 failures: no guidance.
	for i := 0; i < 2; i++ {
		msg := rt.recordEditAttempt("edit_file", args, true)
		if msg != "" {
			t.Fatalf("iteration %d: expected no guidance, got: %s", i, msg)
		}
	}

	// Third failure: soft hint.
	msg := rt.recordEditAttempt("edit_file", args, true)
	if msg == "" {
		t.Fatal("expected soft hint on 3rd failed edit")
	}
	if !strings.Contains(msg, "3 failed edits") {
		t.Errorf("expected '3 failed edits' in message: %s", msg)
	}

	// Fourth failure: no new guidance (soft level already fired).
	msg = rt.recordEditAttempt("edit_file", args, true)
	if msg != "" {
		t.Fatalf("expected no guidance after soft already fired, got: %s", msg)
	}
}

func TestRepetitionTracker_FailedEditHardWarning(t *testing.T) {
	rt := newRepetitionTracker()
	args := json.RawMessage(`{"file_path":"main.go"}`)

	for i := 0; i < 4; i++ {
		rt.recordEditAttempt("edit_file", args, true)
	}

	// 5th failure: hard warning.
	msg := rt.recordEditAttempt("edit_file", args, true)
	if msg == "" {
		t.Fatal("expected hard warning on 5th failed edit")
	}
	if !strings.Contains(msg, "5 failed edits") {
		t.Errorf("expected '5 failed edits' in message: %s", msg)
	}
	if !strings.Contains(msg, "STOP") {
		t.Errorf("expected 'STOP' in hard warning: %s", msg)
	}
}

func TestRepetitionTracker_FailedEditEscalation(t *testing.T) {
	rt := newRepetitionTracker()
	args := json.RawMessage(`{"file_path":"main.go"}`)

	for i := 0; i < 6; i++ {
		rt.recordEditAttempt("edit_file", args, true)
	}

	// 7th failure: escalation.
	msg := rt.recordEditAttempt("edit_file", args, true)
	if msg == "" {
		t.Fatal("expected escalation on 7th failed edit")
	}
	if !strings.Contains(msg, "7 failed edits") {
		t.Errorf("expected '7 failed edits' in message: %s", msg)
	}
	if !strings.Contains(msg, "ask_user") {
		t.Errorf("expected 'ask_user' in escalation: %s", msg)
	}
}

func TestRepetitionTracker_SuccessResetsCount(t *testing.T) {
	rt := newRepetitionTracker()
	args := json.RawMessage(`{"file_path":"main.go"}`)

	// 2 failures.
	rt.recordEditAttempt("edit_file", args, true)
	rt.recordEditAttempt("edit_file", args, true)

	// Success resets.
	rt.recordEditAttempt("edit_file", args, false)

	// 2 more failures — should NOT trigger (count was reset).
	msg := rt.recordEditAttempt("edit_file", args, true)
	if msg != "" {
		t.Fatalf("expected no guidance after reset, got: %s", msg)
	}
	msg = rt.recordEditAttempt("edit_file", args, true)
	if msg != "" {
		t.Fatalf("expected no guidance after reset+2, got: %s", msg)
	}

	// 3rd failure after reset: should trigger soft hint.
	msg = rt.recordEditAttempt("edit_file", args, true)
	if msg == "" {
		t.Fatal("expected soft hint after 3 failures post-reset")
	}
}

func TestRepetitionTracker_DifferentFilesTrackedSeparately(t *testing.T) {
	rt := newRepetitionTracker()
	args1 := json.RawMessage(`{"file_path":"a.go"}`)
	args2 := json.RawMessage(`{"file_path":"b.go"}`)

	// 3 failures on a.go: triggers soft hint.
	msg := rt.recordEditAttempt("edit_file", args1, true)
	if msg != "" {
		t.Fatalf("iteration 1: expected no guidance, got: %s", msg)
	}
	msg = rt.recordEditAttempt("edit_file", args1, true)
	if msg != "" {
		t.Fatalf("iteration 2: expected no guidance, got: %s", msg)
	}
	msg = rt.recordEditAttempt("edit_file", args1, true)
	if msg == "" {
		t.Fatal("expected soft hint for a.go")
	}

	// Failures on b.go are tracked independently.
	msg = rt.recordEditAttempt("edit_file", args2, true)
	if msg != "" {
		t.Fatalf("b.go iteration 1: expected no guidance, got: %s", msg)
	}
}

func TestRepetitionTracker_NonEditToolIgnored(t *testing.T) {
	rt := newRepetitionTracker()
	args := json.RawMessage(`{"path":"main.go"}`)

	// run_command failures should not trigger repetition tracking.
	for i := 0; i < 10; i++ {
		msg := rt.recordEditAttempt("run_command", args, true)
		if msg != "" {
			t.Fatalf("run_command should not trigger repetition: %s", msg)
		}
	}
}

func TestRepetitionTracker_ReadEditFailCycle(t *testing.T) {
	rt := newRepetitionTracker()
	editArgs := json.RawMessage(`{"file_path":"main.go"}`)

	// Create some failed edits first.
	rt.recordEditAttempt("edit_file", editArgs, true)
	rt.recordEditAttempt("edit_file", editArgs, true)

	// Now read the file 3 times — should trigger cycle detection.
	for i := 0; i < 2; i++ {
		msg := rt.recordReadAttempt("main.go")
		if msg != "" {
			t.Fatalf("read iteration %d: expected no guidance, got: %s", i, msg)
		}
	}

	msg := rt.recordReadAttempt("main.go")
	if msg == "" {
		t.Fatal("expected read-edit-fail cycle guidance on 3rd read")
	}
	if !strings.Contains(msg, "read") || !strings.Contains(msg, "main.go") {
		t.Errorf("unexpected cycle message: %s", msg)
	}
}

func TestRepetitionTracker_NormalizePath(t *testing.T) {
	rt := newRepetitionTracker()

	// Edit with "./prefix" path.
	args1 := json.RawMessage(`{"file_path":"./main.go"}`)
	// Edit with bare path.
	args2 := json.RawMessage(`{"file_path":"main.go"}`)

	// 2 failures with "./" prefix.
	rt.recordEditAttempt("edit_file", args1, true)
	rt.recordEditAttempt("edit_file", args1, true)

	// 3rd failure without "./" — should trigger because paths normalize to same.
	msg := rt.recordEditAttempt("edit_file", args2, true)
	if msg == "" {
		t.Fatal("expected soft hint — ./main.go and main.go should be tracked together")
	}
}

func TestRepetitionTracker_Reset(t *testing.T) {
	rt := newRepetitionTracker()
	args := json.RawMessage(`{"file_path":"main.go"}`)

	// Accumulate some failures.
	for i := 0; i < 5; i++ {
		rt.recordEditAttempt("edit_file", args, true)
	}

	// Reset.
	rt.reset()

	// After reset, same number of failures needed to trigger.
	for i := 0; i < 2; i++ {
		msg := rt.recordEditAttempt("edit_file", args, true)
		if msg != "" {
			t.Fatalf("after reset, iteration %d: expected no guidance, got: %s", i, msg)
		}
	}
	msg := rt.recordEditAttempt("edit_file", args, true)
	if msg == "" {
		t.Fatal("expected soft hint after reset + 3 failures")
	}
}

func TestRepetitionTracker_EscalationLevelsNoDoubleFire(t *testing.T) {
	rt := newRepetitionTracker()
	args := json.RawMessage(`{"file_path":"x.go"}`)

	// Accumulate to threshold 7.
	var msgs []string
	for i := 0; i < 7; i++ {
		msg := rt.recordEditAttempt("edit_file", args, true)
		if msg != "" {
			msgs = append(msgs, msg)
		}
	}

	// Should have exactly 3 messages: soft (3), hard (5), escalate (7).
	if len(msgs) != 3 {
		t.Fatalf("expected 3 escalation messages, got %d: %v", len(msgs), msgs)
	}

	// Continue failing — no new messages.
	for i := 0; i < 5; i++ {
		msg := rt.recordEditAttempt("edit_file", args, true)
		if msg != "" {
			t.Fatalf("expected no more guidance after all levels fired, got: %s", msg)
		}
	}
}
