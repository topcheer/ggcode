//go:build goolm

package extpane

// Regression test for GitHub issue #4579: WriteText after HandleDone
// buffered text that flushAll/flushAgent would never flush (both skip
// Done panes), silently dropping late teammate_text events at the 30s
// grace close. Done panes must now write through immediately.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue4579_WriteTextAfterDoneFlushesImmediately(t *testing.T) {
	// No terminal backend needed: the pane is injected directly, the way
	// HandleDone leaves it (Done=true, log file open).
	os.Setenv("TMUX", "")
	os.Setenv("TERM_PROGRAM", "")
	os.Setenv("KITTY_WINDOW_ID", "")
	os.Setenv("LC_TERMINAL", "")

	m := NewManager()
	defer m.CloseAll()
	// WriteText short-circuits on !Available(); satisfy it with a fake
	// backend so the Done branch under test is reachable without a
	// real terminal.
	m.backend = fake4579Backend{}

	logPath := filepath.Join(t.TempDir(), "agent-4579.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.panes["a-4579"] = &ExtPane{AgentID: "a-4579", LogFile: f, LogPath: logPath, Done: true}
	m.mu.Unlock()

	m.WriteText("a-4579", "LATE-AFTER-DONE")

	// Read BEFORE CloseAll: closePane removes the log file by design.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read pane log: %v", err)
	}
	if !strings.Contains(string(data), "LATE-AFTER-DONE") {
		t.Fatal("late WriteText after Done was dropped: buffered, but both flushers skip Done panes (#4579)")
	}
}

type fake4579Backend struct{}

func (fake4579Backend) CreateTab(_ context.Context, _, _ string) (string, error) {
	return "fake-tab", nil
}
func (fake4579Backend) CloseTab(_ string) error { return nil }
func (fake4579Backend) Name() string            { return "fake4579" }
