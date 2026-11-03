package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCommandJobPollingNoDuplicateLines(t *testing.T) {
	workDir := t.TempDir()
	manager := NewCommandJobManager(workDir)

	snapshot, err := manager.Start(context.Background(), "sleep 1 && echo hello", false, 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	sinceLine := 0
	if snapshot != nil {
		sinceLine = snapshot.TotalLines
	}

	var allLines []string
	for {
		current, err := manager.Wait(context.Background(), snapshot.ID, 100*time.Millisecond, 400, sinceLine)
		if err != nil {
			t.Fatalf("Wait failed: %v", err)
		}
		if len(current.Lines) > 0 {
			allLines = append(allLines, current.Lines...)
		}
		sinceLine = current.TotalLines
		if !current.Running {

			if len(allLines) == 0 {
				final, err := manager.Wait(context.Background(), snapshot.ID, 500*time.Millisecond, 400, sinceLine)
				if err != nil {

					break
				}
				if len(final.Lines) > 0 {
					allLines = append(allLines, final.Lines...)
				}
			}
			break
		}
	}

	if len(allLines) != 1 {
		t.Fatalf("expected exactly 1 line total, got %d: %q", len(allLines), allLines)
	}
	if strings.TrimSpace(allLines[0]) != "hello" {
		t.Fatalf("expected 'hello', got %q", allLines[0])
	}
}

func TestCommandJobToolDescriptionsExplainPollingSemantics(t *testing.T) {
	if !strings.Contains(StartCommandTool{}.Description(), "read_command_output") {
		t.Fatalf("start_command description should explain polling, got %q", StartCommandTool{}.Description())
	}
	if !strings.Contains(ReadCommandOutputTool{}.Description(), "since_line") || !strings.Contains(ReadCommandOutputTool{}.Description(), "tail_lines") {
		t.Fatalf("read_command_output description should explain polling semantics, got %q", ReadCommandOutputTool{}.Description())
	}
	if !strings.Contains(WaitCommandTool{}.Description(), "since_line") {
		t.Fatalf("wait_command description should explain incremental polling, got %q", WaitCommandTool{}.Description())
	}
	if !strings.Contains(StopCommandTool{}.Description(), "completed") {
		t.Fatalf("stop_command description should mention completed jobs return an error, got %q", StopCommandTool{}.Description())
	}
	if !strings.Contains(WriteCommandInputTool{}.Description(), "completed job returns an error") {
		t.Fatalf("write_command_input description should explain completed jobs, got %q", WriteCommandInputTool{}.Description())
	}
	if !strings.Contains(ListCommandsTool{}.Description(), "completed jobs retained") {
		t.Fatalf("list_commands description should mention retained completed jobs, got %q", ListCommandsTool{}.Description())
	}

	readParams := string(ReadCommandOutputTool{}.Parameters())
	if !strings.Contains(readParams, "last 1-based Total lines value") || !strings.Contains(readParams, "cap also applies") {
		t.Fatalf("read_command_output schema should clarify since_line/tail_lines semantics: %s", readParams)
	}
	waitParams := string(WaitCommandTool{}.Parameters())
	if !strings.Contains(waitParams, "last 1-based Total lines value") || !strings.Contains(waitParams, "cap also applies") {
		t.Fatalf("wait_command schema should clarify since_line/tail_lines semantics: %s", waitParams)
	}
}
