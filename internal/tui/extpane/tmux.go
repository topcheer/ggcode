package extpane

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// tmuxBackend implements Backend using tmux new-window/kill-window.
// Each agent gets its own full-screen tab, never disturbing the main TUI layout.
type tmuxBackend struct {
	selfWindowID string // window ID of the session where ggcode runs — never close
}

func newTmuxBackend() *tmuxBackend {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}
	b := &tmuxBackend{}
	// Capture our own window ID so we never kill it.
	// $TMUX gives "session:window:pane" format. Extract window as "session:window".
	if raw := os.Getenv("TMUX"); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) >= 2 {
			// tmux uses comma-separated format: ,session,window,pane
			// The format is actually: /tmp/tmux-501/default,12345,0
			// We need to query the active window ID properly
		}
	}
	// Best-effort: query active window ID
	out, err := runTmux(context.Background(), "display-message", "-p", "#{window_id}")
	if err == nil {
		b.selfWindowID = strings.TrimSpace(out)
	}
	return b
}

func (t *tmuxBackend) Name() string { return "tmux" }

// CreateTab creates a new tmux window (full-screen tab) running `tail -f`.
func (t *tmuxBackend) CreateTab(ctx context.Context, title, logfile string) (string, error) {
	// new-window creates a new window (tab), not a split.
	// -P prints info, -F returns just the window ID.
	args := []string{
		"new-window", "-P", "-F", "#{window_id}",
		"-n", title, // window name
		"tail", "-f", logfile,
	}
	output, err := runTmux(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("tmux new-window: %w", err)
	}
	tabID := strings.TrimSpace(output)
	if tabID == "" {
		return "", fmt.Errorf("tmux new-window: empty window ID")
	}
	return tabID, nil
}

// CloseTab kills the tmux window. Refuses to kill our own window.
func (t *tmuxBackend) CloseTab(tabID string) error {
	if tabID == "" || tabID == t.selfWindowID {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runTmux(ctx, "kill-window", "-t", tabID)
	return err
}

// SetTitle renames the window.
func (t *tmuxBackend) SetTitle(tabID, title string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runTmux(ctx, "rename-window", "-t", tabID, title)
	return err
}

func runTmux(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
