package tui

import (
	"strings"
	"testing"
)

func TestContinueTaskEmpty(t *testing.T) {
	m := newTestModel()
	m.crashResumeTask = ""
	if cmd := m.handleContinueTaskCommand(); cmd != nil {
		t.Fatalf("expected nil cmd for empty task, got %v", cmd)
	}
	if m.crashResumeTask != "" {
		t.Fatalf("empty case must not set a task")
	}
}

func TestContinueTaskBusyKeepsTask(t *testing.T) {
	m := newTestModel()
	m.crashResumeTask = "half-finished refactor"
	m.loading = true
	if cmd := m.handleContinueTaskCommand(); cmd != nil {
		t.Fatalf("expected nil cmd when busy, got %v", cmd)
	}
	if m.crashResumeTask != "half-finished refactor" {
		t.Fatalf("busy rejection must NOT consume the resume task (got %q)", m.crashResumeTask)
	}
}

func TestContinueTaskDispatchOneShot(t *testing.T) {
	m := newTestModel()
	m.crashResumeTask = "fix the flaky test"
	if cmd := m.handleContinueTaskCommand(); cmd == nil {
		t.Fatalf("expected a dispatch cmd, got nil")
	}
	if m.crashResumeTask != "" {
		t.Fatalf("dispatch must consume the task (one-shot), still holds %q", m.crashResumeTask)
	}
}

func TestContinueTaskInSlashCommandList(t *testing.T) {
	for _, c := range SlashCommands {
		if c == "/continue-task" {
			if !strings.Contains(SlashCommandDescriptions["/continue-task"], "crash") {
				t.Fatalf("/continue-task description missing 'crash': %q", SlashCommandDescriptions["/continue-task"])
			}

			if ph, ok := SlashCommandPlaceholders["/continue-task"]; !ok || ph != "" {
				t.Fatalf("/continue-task placeholder must exist and be empty (no-arg command), got %q ok=%v", ph, ok)
			}
			return
		}
	}
	t.Fatalf("/continue-task missing from SlashCommands completion list")
}
