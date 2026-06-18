package tool

import (
	"testing"
	"time"
)

func TestSecondsToDuration(t *testing.T) {
	tests := []struct {
		seconds  int
		fallback time.Duration
		expected time.Duration
	}{
		{30, 0, 30 * time.Second},
		{0, time.Minute, time.Minute},
		{60, 0, time.Minute},
		{-1, 5 * time.Minute, 5 * time.Minute},
	}
	for _, tt := range tests {
		got := secondsToDuration(tt.seconds, tt.fallback)
		if got != tt.expected {
			t.Errorf("secondsToDuration(%d, %v) = %v, want %v", tt.seconds, tt.fallback, got, tt.expected)
		}
	}
}

func TestFormatCommandJobSnapshot(t *testing.T) {
	snap := CommandJobSnapshot{
		ID:        "job-1",
		Command:   "echo hello",
		Status:    CommandJobRunning,
		StartedAt: time.Now().Add(-30 * time.Second),
		Duration:  30 * time.Second,
	}
	got := formatCommandJobSnapshot(snap, false)
	if got == "" {
		t.Error("expected non-empty snapshot")
	}
}

func TestSummarizeCommandProgress(t *testing.T) {
	got := summarizeCommandProgress("some result text here")
	if got == "" {
		t.Error("expected non-empty summary")
	}
}

func TestCommandGateIsDestructive(t *testing.T) {
	g := NewCommandGate()
	// With default rules, rm should be destructive
	if !g.IsDestructive("rm -rf /") {
		t.Error("expected 'rm -rf /' to be destructive")
	}
	if g.IsDestructive("echo hello") {
		t.Error("expected 'echo hello' to not be destructive")
	}
}

func TestCommandJobManager_Start_Empty(t *testing.T) {
	m := NewCommandJobManager(t.TempDir())
	_, err := m.Start(nil, "", false, 0)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestCommandJobManager_List_Empty(t *testing.T) {
	m := NewCommandJobManager(t.TempDir())
	list := m.List()
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestAskUserTool_HasHandler(t *testing.T) {
	tool := &AskUserTool{}
	// No handler set
	if tool.HasHandler() {
		t.Error("expected false with no handler")
	}
}
