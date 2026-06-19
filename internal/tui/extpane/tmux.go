package extpane

import (
	"context"
	"fmt"
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
	out, err := runTmux(context.Background(), "display-message", "-p", "#{window_id}")
	if err == nil {
		b.selfWindowID = strings.TrimSpace(out)
	}
	return b
}

func (t *tmuxBackend) Name() string { return "tmux" }

// CreateTab creates a new tmux window (full-screen tab) running `tail -f`.
func (t *tmuxBackend) CreateTab(ctx context.Context, title, logfile string) (string, error) {
	// Temporarily suppress window-created hooks to avoid user tmux configs
	// that trigger interactive rename prompts (e.g. set-hook → command-prompt).
	session, _ := runTmux(ctx, "display-message", "-p", "#{session_name}")
	session = strings.TrimSpace(session)
	if session != "" {
		_, _ = runTmux(ctx, "set-hook", "-t", session, "window-created", "")
	}

	args := []string{
		"new-window", "-P", "-F", "#{window_id}",
		"-n", title,
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

	// Restore user hooks by removing our session-level override.
	if session != "" {
		_, _ = runTmux(ctx, "set-hook", "-t", session, "-u", "window-created")
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
	if tabID == "" || tabID == t.selfWindowID {
		return nil
	}
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
