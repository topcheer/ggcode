package agent

import (
	"strings"
	"testing"
)

func TestSerialRead_BasicStreak(t *testing.T) {
	s := newSerialReadState()

	s.recordToolCall("read_file")
	if w := s.endTurn(1); w != "" {
		t.Fatalf("should not fire on turn 1, got: %s", w)
	}

	s.recordToolCall("grep")
	if w := s.endTurn(2); w != "" {
		t.Fatalf("should not fire on turn 2, got: %s", w)
	}

	s.recordToolCall("git_log")
	w := s.endTurn(3)
	if w == "" {
		t.Fatal("should fire on 3rd consecutive single-read turn")
	}
	if !strings.Contains(w, "Batch Opportunity") {
		t.Fatalf("warning should contain 'Batch Opportunity', got: %s", w)
	}
}

func TestSerialRead_ResetByMutation(t *testing.T) {
	s := newSerialReadState()

	s.recordToolCall("read_file")
	s.endTurn(1)

	s.recordToolCall("grep")
	s.endTurn(2)

	s.recordToolCall("edit_file")
	s.recordToolCall("read_file")
	if w := s.endTurn(3); w != "" {
		t.Fatalf("should not fire when turn has mutation, got: %s", w)
	}

	s.recordToolCall("read_file")
	if w := s.endTurn(4); w != "" {
		t.Fatalf("should not fire right after reset, got: %s", w)
	}
}

func TestSerialRead_ResetByMultipleReads(t *testing.T) {
	s := newSerialReadState()

	s.recordToolCall("read_file")
	s.endTurn(1)

	s.recordToolCall("grep")
	s.endTurn(2)

	s.recordToolCall("read_file")
	s.recordToolCall("grep")
	if w := s.endTurn(3); w != "" {
		t.Fatalf("should not fire on multi-read turn, got: %s", w)
	}

	s.recordToolCall("read_file")
	if w := s.endTurn(4); w != "" {
		t.Fatalf("should be streak=1 after reset, got: %s", w)
	}
}

func TestSerialRead_FiresOncePerRun(t *testing.T) {
	s := newSerialReadState()

	for i := 1; i <= 5; i++ {
		s.recordToolCall("read_file")
		w := s.endTurn(i)
		if i == 3 {
			if w == "" {
				t.Fatal("should fire on turn 3")
			}
		} else if i > 3 && w != "" {
			t.Fatalf("should not fire again on turn %d", i)
		}
	}
}

func TestSerialRead_Reset(t *testing.T) {
	s := newSerialReadState()

	for i := 1; i <= 2; i++ {
		s.recordToolCall("read_file")
		s.endTurn(i)
	}

	s.recordToolCall("lsp_hover")

	s.reset()

	if s.lspStreak != 0 || s.currentTurnTool != "" {
		t.Fatalf("reset should clear lspStreak and currentTurnTool, got lspStreak=%d currentTurnTool=%q", s.lspStreak, s.currentTurnTool)
	}

	for i := 1; i <= 2; i++ {
		s.recordToolCall("read_file")
		if w := s.endTurn(i); w != "" {
			t.Fatalf("should not fire after reset with only %d reads", i)
		}
	}
}

func TestSerialRead_NonReadToolBreaksStreak(t *testing.T) {
	s := newSerialReadState()

	s.recordToolCall("read_file")
	s.endTurn(1)

	s.recordToolCall("grep")
	s.endTurn(2)

	s.recordToolCall("run_command")
	if w := s.endTurn(3); w != "" {
		t.Fatalf("run_command should break streak, got: %s", w)
	}
}

func TestSerialRead_MultiFileReadIsRead(t *testing.T) {
	s := newSerialReadState()

	s.recordToolCall("multi_file_read")
	s.endTurn(1)

	s.recordToolCall("read_file")
	s.endTurn(2)

	s.recordToolCall("git_diff")
	if w := s.endTurn(3); w == "" {
		t.Fatal("multi_file_read and git_diff should count as read-only tools")
	}
}
