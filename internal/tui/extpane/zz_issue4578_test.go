//go:build goolm

package extpane

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue4579_WriteTextAfterDoneFlushesImmediately(t *testing.T) {

	os.Setenv("TMUX", "")
	os.Setenv("TERM_PROGRAM", "")
	os.Setenv("KITTY_WINDOW_ID", "")
	os.Setenv("LC_TERMINAL", "")

	m := NewManager()
	defer m.CloseAll()

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
