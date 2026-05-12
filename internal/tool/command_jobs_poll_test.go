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

	snapshot, err := manager.Start(context.Background(), "echo hello && sleep 1", 0)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	sinceLine := 0
	if snapshot != nil {
		sinceLine = snapshot.TotalLines
	}

	var allLines []string
	for {
		current, err := manager.Wait(context.Background(), snapshot.ID, 50*time.Millisecond, 400, sinceLine)
		if err != nil {
			t.Fatalf("Wait failed: %v", err)
		}
		if len(current.Lines) > 0 {
			allLines = append(allLines, current.Lines...)
		}
		sinceLine = current.TotalLines
		if !current.Running {
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
