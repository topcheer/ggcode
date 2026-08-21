package extpane

import (
	"context"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
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
	// #894: 3s timeout like sibling calls — context.Background() with no
	// deadline let a hung tmux server block TUI model construction forever.
	probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := runTmux(probeCtx, "display-message", "-p", "#{window_id}")
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
	savedHook, showErr := runTmux(ctx, "show-hooks", "-g", "after-new-window")
	savedHook = strings.TrimSpace(savedHook)
	if showErr != nil {
		// #890: previously the error was discarded, leaving savedHook empty,
		// so restore was silently skipped — but the unset below still ran.
		debug.Log("extpane", "show-hooks failed, will still unset+restore best-effort: %v", showErr)
	}
	// Unset the hook globally.
	_, _ = runTmux(ctx, "set-hook", "-g", "-u", "after-new-window")

	// #890: restore via defer — an early return (new-window failure) used
	// to skip the restore entirely, permanently losing the user's GLOBAL
	// hook on the error path.
	restore := func() {
		if savedHook == "" {
			// Nothing was recorded: un-setting was safe only if the hook never
			// existed; leave unset (best effort) — re-query cannot succeed if
			// the initial read failed.
			return
		}
		hookCmd := strings.SplitN(savedHook, " -> ", 2)
		if len(hookCmd) == 2 {
			_, _ = runTmux(context.Background(), "set-hook", "-g", "after-new-window", strings.TrimSpace(hookCmd[1]))
		} else {
			// Unexpected format — restore the raw value so nothing is lost.
			_, _ = runTmux(context.Background(), "set-hook", "-g", "after-new-window", savedHook)
		}
	}
	defer restore()

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
