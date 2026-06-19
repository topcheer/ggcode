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
	// Temporarily suppress after-new-window hook to avoid user tmux configs
	// that trigger interactive rename prompts (command-prompt).
	// Save the current hook value so we can restore it.
	savedHook, _ := runTmux(ctx, "show-hooks", "-g", "after-new-window")
	savedHook = strings.TrimSpace(savedHook)
	if savedHook != "" && !strings.HasPrefix(savedHook, "after-new-window") {
		// show-hooks returns "after-new-window -> ..." format; keep full line for restore
	}
	// Unset the hook globally.
	_, _ = runTmux(ctx, "set-hook", "-g", "-u", "after-new-window")

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

	// Restore the original hook (if any) so user's tmux config still works
	// for manually created windows.
	if savedHook != "" {
		// Extract the command part after "after-new-window -> "
		hookCmd := strings.SplitN(savedHook, " -> ", 2)
		if len(hookCmd) == 2 {
			_, _ = runTmux(ctx, "set-hook", "-g", "after-new-window", strings.TrimSpace(hookCmd[1]))
		}
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
