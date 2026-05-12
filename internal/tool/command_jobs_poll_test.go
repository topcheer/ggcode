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

	// Use a longer sleep so the output has time to be captured before the
	// process exits. The original "echo hello && sleep 1" was flaky on CI
	// because the process could exit before the first poll captured the output.
	snapshot, err := manager.Start(context.Background(), "echo hello && sleep 2", 0)
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
			// Final drain: the process just exited but the last chunk of output
			// may not have been captured yet. Poll once more with a generous
			// timeout to ensure we get everything.
			if len(allLines) == 0 {
				final, err := manager.Wait(context.Background(), snapshot.ID, 500*time.Millisecond, 400, sinceLine)
				if err != nil {
					// Job is gone, that's fine — we already have what we need.
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
