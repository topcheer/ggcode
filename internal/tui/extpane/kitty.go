package extpane

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// kittyBackend implements Backend using kitty remote control.
// Each agent gets its own OS window running `tail -f`.
type kittyBackend struct{}

func newKittyBackend() *kittyBackend {
	if _, err := exec.LookPath("kitten"); err != nil {
		return nil
	}
	return &kittyBackend{}
}

func (k *kittyBackend) Name() string { return "kitty" }

// CreateTab creates a new kitty OS window running `tail -f`.
func (k *kittyBackend) CreateTab(ctx context.Context, title, logfile string) (string, error) {
	args := []string{
		"@", "launch", "--type=window", "--title", title,
		"tail", "-f", logfile,
	}
	output, err := runKitten(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("kitten launch: %w", err)
	}
	windowID := strings.TrimSpace(output)
	if windowID == "" {
		return "", fmt.Errorf("kitten launch: empty window ID")
	}
	return windowID, nil
}

// CloseTab closes the kitty window.
func (k *kittyBackend) CloseTab(tabID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runKitten(ctx, "@", "close-window", "--match=id:"+tabID)
	return err
}

// SetTitle sets the window title.
func (k *kittyBackend) SetTitle(tabID, title string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := runKitten(ctx, "@", "set-window-title", "--match=id:"+tabID, title)
	return err
}

func runKitten(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kitten", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
