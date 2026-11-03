package extpane

import (
	"context"
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"os/exec"
	"strings"
	"time"
)

type tmuxBackend struct {
	selfWindowID string
}

func newTmuxBackend() *tmuxBackend {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}
	b := &tmuxBackend{}

	probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := runTmux(probeCtx, "display-message", "-p", "#{window_id}")
	if err == nil {
		b.selfWindowID = strings.TrimSpace(out)
	}
	return b
}

func (t *tmuxBackend) Name() string { return "tmux" }

func (t *tmuxBackend) CreateTab(ctx context.Context, title, logfile string) (string, error) {

	savedHook, showErr := runTmux(ctx, "show-hooks", "-g", "after-new-window")
	savedHook = strings.TrimSpace(savedHook)

	if !isHookBackupUsable(showErr) {
		debug.Log("extpane", "show-hooks failed, skipping hook suppression to preserve user hook: %v", showErr)
	} else {

		_, _ = runTmux(ctx, "set-hook", "-g", "-u", "after-new-window")
	}

	restore := func() {
		if savedHook == "" {

			return
		}
		hookCmd := strings.SplitN(savedHook, " -> ", 2)

		rctx, rcancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer rcancel()
		if len(hookCmd) == 2 {
			_, _ = runTmux(rctx, "set-hook", "-g", "after-new-window", strings.TrimSpace(hookCmd[1]))
		} else {

			_, _ = runTmux(rctx, "set-hook", "-g", "after-new-window", savedHook)
		}
	}
	defer restore()

	args := []string{

		"new-window", "-d", "-P", "-F", "#{window_id}",
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

func (t *tmuxBackend) CloseTab(tabID string) error {
	if tabID == "" || tabID == t.selfWindowID {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runTmux(ctx, "kill-window", "-t", tabID)
	return err
}

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
